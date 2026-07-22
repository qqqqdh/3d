package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/net/websocket"
)

// Point2D represents a 2D coordinate on an image plane.
type Point2D struct {
	U          float64 `json:"u"`
	V          float64 `json:"v"`
	Visibility float64 `json:"visibility"`
}

// Point3D represents a 3D coordinate in world space.
type Point3D struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z"`
}

// Matrix3x4 represents a 3x4 projection matrix.
type Matrix3x4 [3][4]float64

// CameraData is the payload sent by camera clients.
type CameraData struct {
	Type             string    `json:"type"`
	CameraID         int       `json:"camera_id"`
	FrameIndex       int64     `json:"frame_index"`
	Timestamp        int64     `json:"timestamp"`
	Landmarks        []Point2D `json:"landmarks"`
	ProjectionMatrix Matrix3x4 `json:"projection_matrix"`
}

// JointConstraint defines the min and max angles for warning triggers.
type JointConstraint struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

// ClientConfig is received from the dashboard.
type ClientConfig struct {
	Type        string                     `json:"type"`
	EMAAlpha    float64                    `json:"ema_alpha"`
	Constraints map[string]JointConstraint `json:"constraints"`
}

// DashboardMessage is sent from the server to all dashboards.
type DashboardMessage struct {
	Type       string             `json:"type"`
	FrameIndex int64              `json:"frame_index"`
	Timestamp  int64              `json:"timestamp"`
	Landmarks  []Point3D          `json:"landmarks"`
	Angles     map[string]float64 `json:"angles"`
	Warnings   map[string]bool    `json:"warnings"`
}

// StatusMessage is sent from the server to dashboards when active cameras count changes.
type StatusMessage struct {
	Type          string `json:"type"`
	ActiveCount   int    `json:"active_count"`
	ActiveCameras []int  `json:"active_cameras"`
	Message       string `json:"message"`
}

// Global System Configuration and State
type SystemState struct {
	mu          sync.RWMutex
	dashboards  map[*websocket.Conn]bool
	alpha       float64
	constraints map[string]JointConstraint
	
	// Previous smoothed skeleton state for EMA: index -> Point3D
	smoothedLandmarks []Point3D
	hasSmoothedState  bool

	// Synchronization buffer: frame_index -> camera_id -> CameraData
	syncBuffer map[int64]map[int]CameraData

	// Active cameras tracking
	activeCameras map[int]time.Time
}

var state = SystemState{
	dashboards: make(map[*websocket.Conn]bool),
	alpha:      0.3, // Default EMA smoothing factor
	constraints: map[string]JointConstraint{
		"left_knee":   {Min: 70, Max: 180},
		"right_knee":  {Min: 70, Max: 180},
		"left_elbow":  {Min: 50, Max: 180},
		"right_elbow": {Min: 50, Max: 180},
		"left_hip":    {Min: 60, Max: 180},
		"right_hip":   {Min: 60, Max: 180},
	},
	syncBuffer:    make(map[int64]map[int]CameraData),
	activeCameras: make(map[int]time.Time),
}

func main() {
	// 1. Load Configurations from Environment Variables
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	if !strings.HasPrefix(port, ":") {
		port = ":" + port
	}

	if defaultAlphaStr := os.Getenv("DEFAULT_EMA_ALPHA"); defaultAlphaStr != "" {
		if val, err := strconv.ParseFloat(defaultAlphaStr, 64); err == nil && val > 0 && val <= 1.0 {
			state.alpha = val
			log.Printf("Loaded default EMA alpha: %.2f\n", val)
		}
	}

	// 2. Set Up HTTP Route Handlers
	mux := http.NewServeMux()
	fs := http.FileServer(http.Dir("."))
	mux.Handle("/", fs)
	mux.Handle("/ws", websocket.Handler(wsHandler))

	server := &http.Server{
		Addr:    port,
		Handler: mux,
	}

	// 3. Start Server in a separate Goroutine
	go func() {
		log.Printf("Starting production pose 3D server on http://localhost%s\n", port)
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatalf("Server ListenAndServe failed: %v", err)
		}
	}()

	// 4. Implement Graceful Shutdown Handling
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Println("Shutting down server gracefully...")

	// Create a timeout context for the shutdown process
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Safely close all active WebSocket connections
	state.mu.Lock()
	log.Printf("Closing %d active WebSocket client connections...\n", len(state.dashboards))
	for ws := range state.dashboards {
		ws.Close()
		delete(state.dashboards, ws)
	}
	state.mu.Unlock()

	// Shutdown the HTTP server
	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	log.Println("Server exited cleanly.")
}

func wsHandler(ws *websocket.Conn) {
	state.mu.Lock()
	state.dashboards[ws] = true
	state.mu.Unlock()

	log.Printf("New WebSocket client connected: %s\n", ws.RemoteAddr())

	defer func() {
		state.mu.Lock()
		delete(state.dashboards, ws)
		state.mu.Unlock()
		ws.Close()
		log.Printf("WebSocket client disconnected: %s\n", ws.RemoteAddr())
	}()

	dec := json.NewDecoder(ws)
	for {
		var rawMsg map[string]interface{}
		if err := dec.Decode(&rawMsg); err != nil {
			break // connection closed or invalid format
		}

		msgType, _ := rawMsg["type"].(string)

		if msgType == "camera_data" {
			var camData CameraData
			dataBytes, err := json.Marshal(rawMsg)
			if err != nil {
				continue
			}
			if err := json.Unmarshal(dataBytes, &camData); err == nil {
				handleCameraData(camData)
			}
		} else if msgType == "config" {
			var config ClientConfig
			dataBytes, err := json.Marshal(rawMsg)
			if err != nil {
				continue
			}
			if err := json.Unmarshal(dataBytes, &config); err == nil {
				state.mu.Lock()
				if config.EMAAlpha > 0 && config.EMAAlpha <= 1.0 {
					state.alpha = config.EMAAlpha
				}
				if config.Constraints != nil {
					for k, v := range config.Constraints {
						state.constraints[k] = v
					}
				}
				state.mu.Unlock()
				log.Println("Updated server configuration from dashboard client.")
			}
		}
	}
}

// handleCameraData processes incoming 2D camera coordinates and attempts 3D reconstruction.
func handleCameraData(data CameraData) {
	state.mu.Lock()
	defer state.mu.Unlock()

	// Track active camera timestamp
	if state.activeCameras == nil {
		state.activeCameras = make(map[int]time.Time)
	}
	state.activeCameras[data.CameraID] = time.Now()

	// Ensure the inner map exists for this frame index
	if _, ok := state.syncBuffer[data.FrameIndex]; !ok {
		state.syncBuffer[data.FrameIndex] = make(map[int]CameraData)
	}
	state.syncBuffer[data.FrameIndex][data.CameraID] = data

	// Count cameras active in the last 3 seconds
	activeCount := 0
	var activeList []int
	now := time.Now()
	for id, lastSeen := range state.activeCameras {
		if now.Sub(lastSeen) < 3*time.Second {
			activeCount++
			activeList = append(activeList, id)
		}
	}

	framesAvailable := state.syncBuffer[data.FrameIndex]
	if len(framesAvailable) >= 3 {
		// Reconstruct and clean up
		reconstructPose(data.FrameIndex, framesAvailable)
		cleanBuffer(data.FrameIndex)
	} else {
		// Fallback for dropped frames: if a newer frame index has arrived,
		// run 2-camera triangulation on the older buffered frame indices.
		reconstructed := false
		for idx, cams := range state.syncBuffer {
			if idx < data.FrameIndex && len(cams) >= 2 {
				reconstructPose(idx, cams)
				cleanBuffer(idx)
				reconstructed = true
			}
		}

		// If no reconstruction could happen and we have less than 2 active cameras,
		// broadcast a status warning.
		if !reconstructed && activeCount < 2 {
			msg := StatusMessage{
				Type:          "status",
				ActiveCount:   activeCount,
				ActiveCameras: activeList,
				Message:       fmt.Sprintf("3D 복원을 위해서는 최소 2대 이상의 카메라가 데이터를 전송해야 합니다. (현재 연결된 카메라: %d대)", activeCount),
			}
			msgBytes, _ := json.Marshal(msg)
			for client := range state.dashboards {
				go func(c *websocket.Conn) {
					_, _ = c.Write(msgBytes)
				}(client)
			}
		}
	}
}

// cleanBuffer removes the frame index and any obsolete indices from the sync buffer.
func cleanBuffer(frameIndex int64) {
	delete(state.syncBuffer, frameIndex)
	for idx := range state.syncBuffer {
		if idx < frameIndex-100 {
			delete(state.syncBuffer, idx)
		}
	}
}

// reconstructPose performs 3D triangulation, EMA smoothing, joint angle calculations, and sends the result to the dashboards.
func reconstructPose(frameIndex int64, cameraFrames map[int]CameraData) {
	var numLandmarks int
	var timestamp int64
	for _, f := range cameraFrames {
		numLandmarks = len(f.Landmarks)
		timestamp = f.Timestamp
		break
	}

	if numLandmarks == 0 {
		return
	}

	rawLandmarks3D := make([]Point3D, numLandmarks)

	// Perform Triangulation for each landmark
	for j := 0; j < numLandmarks; j++ {
		var points []Point2D
		var matrices []Matrix3x4

		for _, frame := range cameraFrames {
			if j < len(frame.Landmarks) {
				lm := frame.Landmarks[j]
				if lm.Visibility > 0.5 {
					points = append(points, lm)
					matrices = append(matrices, frame.ProjectionMatrix)
				}
			}
		}

		if len(points) >= 2 {
			rawLandmarks3D[j] = Triangulate(points, matrices)
		} else {
			if state.hasSmoothedState && j < len(state.smoothedLandmarks) {
				rawLandmarks3D[j] = state.smoothedLandmarks[j]
			} else {
				rawLandmarks3D[j] = Point3D{0, 0, 0}
			}
		}
	}

	// Apply EMA Smoothing
	smoothedLandmarks3D := make([]Point3D, numLandmarks)
	if !state.hasSmoothedState || len(state.smoothedLandmarks) != numLandmarks {
		copy(smoothedLandmarks3D, rawLandmarks3D)
		state.smoothedLandmarks = make([]Point3D, numLandmarks)
		copy(state.smoothedLandmarks, rawLandmarks3D)
		state.hasSmoothedState = true
	} else {
		for j := 0; j < numLandmarks; j++ {
			smoothedLandmarks3D[j].X = state.alpha*rawLandmarks3D[j].X + (1-state.alpha)*state.smoothedLandmarks[j].X
			smoothedLandmarks3D[j].Y = state.alpha*rawLandmarks3D[j].Y + (1-state.alpha)*state.smoothedLandmarks[j].Y
			smoothedLandmarks3D[j].Z = state.alpha*rawLandmarks3D[j].Z + (1-state.alpha)*state.smoothedLandmarks[j].Z
		}
		copy(state.smoothedLandmarks, smoothedLandmarks3D)
	}

	// Compute Joint Angles
	angles := make(map[string]float64)
	warnings := make(map[string]bool)

	getLM := func(idx int) Point3D {
		if idx >= 0 && idx < len(smoothedLandmarks3D) {
			return smoothedLandmarks3D[idx]
		}
		return Point3D{0, 0, 0}
	}

	angles["left_knee"] = CalculateAngle(getLM(23), getLM(25), getLM(27))
	angles["right_knee"] = CalculateAngle(getLM(24), getLM(26), getLM(28))
	angles["left_elbow"] = CalculateAngle(getLM(11), getLM(13), getLM(15))
	angles["right_elbow"] = CalculateAngle(getLM(12), getLM(14), getLM(16))
	angles["left_hip"] = CalculateAngle(getLM(11), getLM(23), getLM(25))
	angles["right_hip"] = CalculateAngle(getLM(12), getLM(24), getLM(26))

	// Check constraints
	for jointName, val := range angles {
		if limit, ok := state.constraints[jointName]; ok {
			if val < limit.Min || val > limit.Max {
				warnings[jointName] = true
			} else {
				warnings[jointName] = false
			}
		}
	}

	// Broadcast payload
	msg := DashboardMessage{
		Type:       "pose_3d",
		FrameIndex: frameIndex,
		Timestamp:  timestamp,
		Landmarks:  smoothedLandmarks3D,
		Angles:     angles,
		Warnings:   warnings,
	}

	msgBytes, err := json.Marshal(msg)
	if err != nil {
		return
	}

	for client := range state.dashboards {
		go func(c *websocket.Conn) {
			_, _ = c.Write(msgBytes)
		}(client)
	}
}

// Triangulate reconstructs a 3D point from multiple 2D camera observations.
// It solves normal equations (A^T A) X = A^T B via Cramer's Rule.
func Triangulate(points []Point2D, matrices []Matrix3x4) Point3D {
	n := len(points)
	if n < 2 {
		return Point3D{0, 0, 0}
	}

	A := make([][3]float64, 2*n)
	B := make([]float64, 2*n)

	for i := 0; i < n; i++ {
		u := points[i].U
		v := points[i].V
		P := matrices[i]

		A[2*i][0] = u*P[2][0] - P[0][0]
		A[2*i][1] = u*P[2][1] - P[0][1]
		A[2*i][2] = u*P[2][2] - P[0][2]
		B[2*i] = P[0][3] - u*P[2][3]

		A[2*i+1][0] = v*P[2][0] - P[1][0]
		A[2*i+1][1] = v*P[2][1] - P[1][1]
		A[2*i+1][2] = v*P[2][2] - P[1][2]
		B[2*i+1] = P[1][3] - v*P[2][3]
	}

	var M [3][3]float64
	var C [3]float64
	for r := 0; r < 3; r++ {
		for c := 0; c < 3; c++ {
			sum := 0.0
			for k := 0; k < 2*n; k++ {
				sum += A[k][r] * A[k][c]
			}
			M[r][c] = sum
		}
		sumB := 0.0
		for k := 0; k < 2*n; k++ {
			sumB += A[k][r] * B[k]
		}
		C[r] = sumB
	}

	det := M[0][0]*(M[1][1]*M[2][2]-M[1][2]*M[2][1]) -
		M[0][1]*(M[1][0]*M[2][2]-M[1][2]*M[2][0]) +
		M[0][2]*(M[1][0]*M[2][1]-M[1][1]*M[2][0])

	if math.Abs(det) < 1e-9 {
		return Point3D{0, 0, 0}
	}

	detX := C[0]*(M[1][1]*M[2][2]-M[1][2]*M[2][1]) -
		M[0][1]*(C[1]*M[2][2]-M[1][2]*C[2]) +
		M[0][2]*(C[1]*M[2][1]-M[1][1]*C[2])

	detY := M[0][0]*(C[1]*M[2][2]-M[1][2]*C[2]) -
		C[0]*(M[1][0]*M[2][2]-M[1][2]*M[2][0]) +
		M[0][2]*(M[1][0]*C[2]-C[1]*M[2][0])

	detZ := M[0][0]*(M[1][1]*C[2]-C[1]*M[2][1]) -
		M[0][1]*(M[1][0]*C[2]-C[1]*M[2][0]) +
		C[0]*(M[1][0]*M[2][1]-M[1][1]*M[2][0])

	return Point3D{
		X: detX / det,
		Y: detY / det,
		Z: detZ / det,
	}
}

// CalculateAngle calculates the 3D angle ABC at joint vertex B in degrees.
func CalculateAngle(A, B, C Point3D) float64 {
	vBA := Point3D{X: A.X - B.X, Y: A.Y - B.Y, Z: A.Z - B.Z}
	vBC := Point3D{X: C.X - B.X, Y: C.Y - B.Y, Z: C.Z - B.Z}

	dot := vBA.X*vBC.X + vBA.Y*vBC.Y + vBA.Z*vBC.Z
	lenBA := math.Sqrt(vBA.X*vBA.X + vBA.Y*vBA.Y + vBA.Z*vBA.Z)
	lenBC := math.Sqrt(vBC.X*vBC.X + vBC.Y*vBC.Y + vBC.Z*vBC.Z)

	if lenBA < 1e-6 || lenBC < 1e-6 {
		return 0.0
	}

	cosTheta := dot / (lenBA * lenBC)
	if cosTheta > 1.0 {
		cosTheta = 1.0
	} else if cosTheta < -1.0 {
		cosTheta = -1.0
	}

	rad := math.Acos(cosTheta)
	if math.IsNaN(rad) {
		return 0.0
	}
	return rad * 180.0 / math.Pi
}
