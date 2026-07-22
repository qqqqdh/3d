package main

import (
	"math"
	"testing"
)

// Helper to normalize a 3D vector.
func normalize(v Point3D) Point3D {
	length := math.Sqrt(v.X*v.X + v.Y*v.Y + v.Z*v.Z)
	if length == 0 {
		return Point3D{0, 0, 0}
	}
	return Point3D{v.X / length, v.Y / length, v.Z / length}
}

// Helper to calculate cross product of two vectors.
func cross(a, b Point3D) Point3D {
	return Point3D{
		X: a.Y*b.Z - a.Z*b.Y,
		Y: a.Z*b.X - a.X*b.Z,
		Z: a.X*b.Y - a.Y*b.X,
	}
}

// Helper to dot two vectors.
func dot(a, b Point3D) float64 {
	return a.X*b.X + a.Y*b.Y + a.Z*b.Z
}

// Helper to generate Projection Matrix from camera position, target, and focal length.
func makeProjectionMatrix(camPos, target Point3D, f, cx, cy float64) Matrix3x4 {
	// 1. Compute camera axes
	zcam := normalize(Point3D{X: camPos.X - target.X, Y: camPos.Y - target.Y, Z: camPos.Z - target.Z})
	yworld := Point3D{0, 1, 0}
	xcam := normalize(cross(yworld, zcam))
	ycam := cross(zcam, xcam)

	// 2. Rotation matrix R (rows are xcam, ycam, zcam)
	R := [3][3]float64{
		{xcam.X, xcam.Y, xcam.Z},
		{ycam.X, ycam.Y, ycam.Z},
		{zcam.X, zcam.Y, zcam.Z},
	}

	// 3. Translation T = -R * camPos
	T := [3]float64{
		-(R[0][0]*camPos.X + R[0][1]*camPos.Y + R[0][2]*camPos.Z),
		-(R[1][0]*camPos.X + R[1][1]*camPos.Y + R[1][2]*camPos.Z),
		-(R[2][0]*camPos.X + R[2][1]*camPos.Y + R[2][2]*camPos.Z),
	}

	// 4. Intrinsic K
	K := [3][3]float64{
		{f, 0, cx},
		{0, f, cy},
		{0, 0, 1},
	}

	// 5. P = K * [R | T]
	var P Matrix3x4
	for r := 0; r < 3; r++ {
		for c := 0; c < 3; c++ {
			P[r][c] = K[r][0]*R[0][c] + K[r][1]*R[1][c] + K[r][2]*R[2][c]
		}
		P[r][3] = K[r][0]*T[0] + K[r][1]*T[1] + K[r][2]*T[2]
	}

	return P
}

// Project a 3D point to 2D image coordinates.
func project(pt Point3D, P Matrix3x4) Point2D {
	w := P[2][0]*pt.X + P[2][1]*pt.Y + P[2][2]*pt.Z + P[2][3]
	u := (P[0][0]*pt.X + P[0][1]*pt.Y + P[0][2]*pt.Z + P[0][3]) / w
	v := (P[1][0]*pt.X + P[1][1]*pt.Y + P[1][2]*pt.Z + P[1][3]) / w
	return Point2D{U: u, V: v, Visibility: 1.0}
}

func TestTriangulate(t *testing.T) {
	// 1. Define True 3D Point
	Xtrue := Point3D{X: 0.5, Y: 1.2, Z: -0.3}

	// Camera Intrinsic parameters
	f := 800.0
	cx := 320.0
	cy := 240.0
	target := Point3D{0, 1, 0}

	// 2. Define 3 Cameras
	cam1 := Point3D{0, 1, 3}      // Front camera
	cam2 := Point3D{3, 1, 0}      // Side camera
	cam3 := Point3D{-2, 1.5, 2}   // Diagonal camera

	P1 := makeProjectionMatrix(cam1, target, f, cx, cy)
	P2 := makeProjectionMatrix(cam2, target, f, cx, cy)
	P3 := makeProjectionMatrix(cam3, target, f, cx, cy)

	// 3. Project 3D Point to each camera's 2D plane
	x1 := project(Xtrue, P1)
	x2 := project(Xtrue, P2)
	x3 := project(Xtrue, P3)

	points := []Point2D{x1, x2, x3}
	matrices := []Matrix3x4{P1, P2, P3}

	// 4. Perform Triangulation
	Xest := Triangulate(points, matrices)

	// 5. Assert distance is very close to zero
	dx := Xest.X - Xtrue.X
	dy := Xest.Y - Xtrue.Y
	dz := Xest.Z - Xtrue.Z
	dist := math.Sqrt(dx*dx + dy*dy + dz*dz)

	t.Logf("True point: %+v", Xtrue)
	t.Logf("Estimated point: %+v", Xest)
	t.Logf("Reconstruction Error: %g meters", dist)

	if dist > 1e-5 {
		t.Errorf("Triangulation failed. Reconstructed point too far from original. Error: %g", dist)
	}

	// 6. Test with only 2 cameras (Camera 1 & Camera 2)
	Xest2 := Triangulate(points[:2], matrices[:2])
	dx2 := Xest2.X - Xtrue.X
	dy2 := Xest2.Y - Xtrue.Y
	dz2 := Xest2.Z - Xtrue.Z
	dist2 := math.Sqrt(dx2*dx2 + dy2*dy2 + dz2*dz2)
	t.Logf("Estimated (2 cameras): %+v, Error: %g meters", Xest2, dist2)
	if dist2 > 1e-5 {
		t.Errorf("2-camera triangulation failed. Error: %g", dist2)
	}
}

func TestCalculateAngle(t *testing.T) {
	// Right angle (90 degrees)
	A := Point3D{1, 0, 0}
	B := Point3D{0, 0, 0}
	C := Point3D{0, 1, 0}

	angle := CalculateAngle(A, B, C)
	t.Logf("Computed Angle (90 Expected): %f", angle)
	if math.Abs(angle-90.0) > 1e-5 {
		t.Errorf("Angle calculation failed. Expected 90.0, got %f", angle)
	}

	// Straight line (180 degrees)
	A2 := Point3D{-1, 0, 0}
	B2 := Point3D{0, 0, 0}
	C2 := Point3D{1, 0, 0}

	angle2 := CalculateAngle(A2, B2, C2)
	t.Logf("Computed Angle (180 Expected): %f", angle2)
	if math.Abs(angle2-180.0) > 1e-5 {
		t.Errorf("Angle calculation failed. Expected 180.0, got %f", angle2)
	}
}
