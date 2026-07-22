# 실시간 3D 자세 복원 및 운동 피드백 시스템 (Production Ready)

본 프로젝트는 고가의 3D 모션 캡처 장비 없이 일반 스마트폰 카메라 3대(또는 가상 시뮬레이터 및 웹캠)를 이용해 3차원 공간에서 사용자의 자세를 정밀하게 복원하고, 웹 브라우저에서 실시간으로 시각화 및 피드백할 수 있는 상용 배포 가능한 수준의 풀스택 솔루션입니다.

---

## 🏗️ 시스템 아키텍처

```
+------------------------------------------+
|  클라이언트 기기 (스마트폰 / 웹캠 브라우저)  |
|  - camera.html (MediaPipe Pose 2D 검출)   |
+------------------------------------------+
                    | (Websocket: 2D 랜드마크 + 투영 행렬)
                    v
+------------------------------------------+
|  Go 중앙 백엔드 서버 (server.go)             |
|  - 타임스탬프 기반 다중 피드 동기화 버퍼      |
|  - 최소자승법(LSM) 삼각측량 (크래머 공식 해)  |
|  - EMA 지수 이동 평균 노이즈 스무딩 필터     |
|  - 실시간 관절 사이각 계산 및 한계 검출       |
+------------------------------------------+
                    | (Websocket: Reconstructed 3D Pose + Warnings)
                    v
+------------------------------------------+
|  웹 통합 모니터링 대시보드 (index.html)      |
|  - Three.js 3D 캐릭터 스켈레톤 렌더링       |
|  - 실시간 네온 경고 및 원형 게이지           |
|  - 실시간 자세 트렌드 꺾은선 차트 (Chart.js) |
|  - 운동 세션 시계열 데이터 저장 (JSON)       |
|  - 가상 카메라 모션 시뮬레이터 내장          |
+------------------------------------------+
```

---

## 🚀 실행 및 테스트 방법

### 1. 로컬 환경에서 실행 (Go 설치 필요)
Go 언어가 설치된 로컬 터미널에서 다음 명령어를 실행합니다.
```bash
# 의존성 패키지 다운로드 및 서버 실행
go run server.go
```
서버가 성공적으로 실행되면 웹 브라우저를 열어 다음 주소로 접속합니다:
- **대시보드 화면**: [http://localhost:8080](http://localhost:8080)
- **웹캠 촬영 스트리머 화면**: [http://localhost:8080/camera.html](http://localhost:8080/camera.html)

---

### 2. Docker로 빌드 및 실행 (컨테이너화)
Dockerfile은 멀티스테이지 빌드를 통해 **단 15MB 내외의 고도로 최적화된 알파인 컨테이너 이미지**로 빌드됩니다.
```bash
# Docker 이미지 빌드
docker build -t pose3d-server .

# 컨테이너 실행 (8080 포트 포워딩)
docker run -d -p 8080:8080 --name pose3d-instance pose3d-server
```

---

### 3. Docker Compose로 통합 실행
개발 및 프로덕션 환경의 간편한 운영을 위해 Docker Compose 설정을 제공합니다.
```bash
# 백그라운드 빌드 및 컨테이너 기동
docker-compose up --build -d

# 실행 로그 실시간 확인
docker-compose logs -f

# 서비스 중지
docker-compose down
```

---

## ⚙️ 프로덕션 설정 (환경 변수)

서버 구동 시 다음과 같은 환경 변수를 주입하여 설정값을 편리하게 제어할 수 있습니다.

| 환경 변수명 | 기본값 | 설명 |
| :--- | :--- | :--- |
| `PORT` | `8080` | Go 웹 서버 및 WebSocket 포트 번호 |
| `DEFAULT_EMA_ALPHA` | `0.3` | 삼각측량 후 노이즈를 제어하기 위한 지수 이동 평균(EMA) 기본값 ($0 < \alpha \le 1.0$) |

---

## 📝 웹소켓 프로토콜 구조 (API 명세)

Go 서버와 각 웹 클라이언트는 포트 `8080/ws` 상에서 JSON 형태로 양방향 통신합니다.

### 1. 카메라 2D 데이터 전송 (Client -> Server)
```json
{
  "type": "camera_data",
  "camera_id": 1,
  "frame_index": 120,
  "timestamp": 1690000000000,
  "projection_matrix": [
    [800.0, 0.0, 320.0, 0.0],
    [0.0, 800.0, 240.0, -800.0],
    [0.0, 0.0, 1.0, -3.0]
  ],
  "landmarks": [
    {"u": 320.5, "v": 240.1, "visibility": 0.95},
    {"u": 322.0, "v": 238.4, "visibility": 0.98}
  ]
}
```

### 2. 자세 제어 및 임계치 설정 (Dashboard -> Server)
```json
{
  "type": "config",
  "ema_alpha": 0.3,
  "constraints": {
    "left_knee": {"min": 70.0, "max": 180.0},
    "right_knee": {"min": 70.0, "max": 180.0}
  }
}
```

### 3. 복원 3D 좌표 및 피드백 브로드캐스팅 (Server -> Dashboards)
```json
{
  "type": "pose_3d",
  "frame_index": 120,
  "timestamp": 1690000000000,
  "landmarks": [
    {"x": 0.012, "y": 1.452, "z": -0.054},
    {"x": -0.198, "y": 1.341, "z": -0.012}
  ],
  "angles": {
    "left_knee": 115.42,
    "right_knee": 116.10,
    "left_elbow": 172.50
  },
  "warnings": {
    "left_knee": false,
    "right_knee": false,
    "left_elbow": false
  }
}
```

---

## 🌟 프로덕션 기능 하이라이트
- **Graceful Shutdown**: `SIGINT`, `SIGTERM` 이벤트를 가로채어 실행 중인 WebSocket 클라이언트 연결을 정리하고 HTTP 서버를 안전하게 종료합니다.
- **Occlusion Fallback**: 3대 중 특정 카메라에서 빛 번짐, 신체 가림으로 관절 검출률(`visibility <= 0.5`)이 낮아질 시, 작동하는 2대의 카메라 좌표만을 이용해 실시간 $4 \times 3$ 최소자승 정규방정식을 계산하여 끊김 없는 자세 복원을 진행합니다.
- **Trend Chart**: Chart.js가 실시간 트렌드 그래프를 렌더링하며 화면 프레임 레이트 저하를 방지하기 위해 애니메이션 엔진을 비활성화하고 무지연 렌더링을 구현하였습니다.
- **Exporting Session**: 운동 완료 후 사용자의 실시간 복원 3D 좌표, 관절 각도, 경고 타임스탬프 기록 전체를 JSON 파일로 간편히 로컬로 다운로드할 수 있어 사후 분석에 활용할 수 있습니다.
