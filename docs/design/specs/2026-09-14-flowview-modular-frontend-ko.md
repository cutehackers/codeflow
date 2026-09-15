# FlowView 모듈형 프론트엔드 아키텍처 스펙

- Contract ID: `FLOWVIEW-MODULAR-FRONTEND`
- Contract Status: Approved
- Created: 2026-09-14
- Intent Status: Hardened
- Source: 사용자 요청 ("FlowView는 완전 live-semantic-map-prototype.html 으로 탈피하는 것이다", "하드코드된 화면을 더 이상 사용하지 않고 reusable하고 모듈화된 유지보수 관리 가능한 형태의 화면을 작성하려는 계획")
- Decision Records: [`docs/design/decisions/2026-09-14-flowview-modular-frontend-ko.md`](../decisions/2026-09-14-flowview-modular-frontend-ko.md)
- Glossary: [`docs/design/glossary.md`](../glossary.md)

---

## 1. 문제와 목표

기존 FlowView는 4,200줄에 달하는 레거시 화면(`flow_view.html`)의 복잡성과, 260여 줄에 축약·압축된 채 하드코딩이 섞여 있던 프로토타입(`live-semantic-map-prototype.html`)으로 인해 유지보수와 기능 확장이 어려웠다.
CodeFlow가 기존 레거시를 탈피하여 `live-semantic-map-prototype.html`을 표준 단일 기반 화면으로 전면 승격함에 따라, `live-semantic-map-final.html`에서 검증된 **MACRO CONTEXT STORYBOARD**(거시 비즈니스 관문 조망)와 **BLAST RADIUS RADAR**(3중 동심원 파급 영향 레이더)를 깔끔하게 통합해야 한다.

이 스펙의 목표는 외부 무거운 번들러 도입 없이 브라우저 표준 기술을 활용하여 **하드코딩을 배제하고, 상태와 컴포넌트가 명확히 분리된 재사용 가능하며 유지보수 가능한 단일 파일 모듈형 프론트엔드 아키텍처**를 구축하는 것이다.

### Intent 및 Goal 정의
* **INT-FRONTEND-01**: 레거시의 복잡도를 탈피하고 가볍고 재사용 가능한 단일 FlowView 프론트엔드를 구축한다.
* **GOAL-FRONTEND-01**: `live-semantic-map-prototype.html`을 컴포넌트 기반 아키텍처(단일 상태 스토어, 독립 렌더러)로 리팩토링한다.
* **GOAL-FRONTEND-02**: 상단에 엔드투엔드 처리 단계를 조망하는 `MACRO CONTEXT STORYBOARD`를 구축하고 양방향 동기화를 구현한다.
* **GOAL-FRONTEND-03**: 선택/수정된 심볼의 호출자 및 테스트 파급 범위를 시각화하는 `BLAST RADIUS RADAR` 순수 SVG 컴포넌트를 구축하고 `/api/task/impact`와 연동한다.
* **GOAL-FRONTEND-04**: 기존 Go embed 및 `scripts/generate_live_semantic.py` 파이프라인, 테스트 계약과의 100% 호환성을 보존한다.

---

## 2. 범위 (Scope)

### 포함 범위 (In Scope)
1. **표준 템플릿 현대화 (`docs/samples/live-semantic-map-prototype.html`)**:
   * 네임스페이스 기반 컴포넌트 모듈 분리 (`CodeFlow.Store`, `CodeFlow.Components.*`, `CodeFlow.API`)
   * 세련된 반응형 레이아웃 (CSS Grid & Flexbox 기반 3단 워크벤치 + 상단 스토리보드)
2. **MACRO CONTEXT STORYBOARD 컴포넌트**:
   * `semanticMap.steps` 기반 동적 가로 시퀀스 카드 트랙 렌더링
   * 아키텍처 계층 배지(`FRAME 01 · UI EVENT`) 및 변경 배지(`~ RULE CHG`, `+ NEW SURGERY`)
   * 스토리 카드 클릭 시 중앙 코드 및 좌측 레일과의 양방향 스크롤/하이라이트 동기화
3. **BLAST RADIUS RADAR 컴포넌트**:
   * 순수 SVG 기반 3중 동심원 레이더 (중심: 타겟 심볼, 1차 링: 직접 호출자/상태 변경, 2차 링: 간접 호출자, 3차 링: 테스트)
   * 극좌표-직교좌표 자동 배치 알고리즘 (충돌 없는 노드 분산)
   * 오프라인/샘플 모드용 Mock 데이터 및 실제 라이브 환경의 `GET /api/task/impact` 비동기 조회 결합
   * 테스트 부재 또는 API 오류 시 직접 연결(`semanticMap.edges`) 기반 폴백 렌더링
4. **빌드 파이프라인 및 테스트 보존**:
   * `// SAMPLE_BOOTSTRAP` 마커를 기준으로 독립 샘플 시나리오와 제품 런타임 분리 유지
   * `TestLiveViewMatchesDesignatedTemplate` 테스트 100% 통과

### 제외 범위 (Non-Goals)
* React, Vue, Svelte, Webpack, Vite 등 외부 라이브러리/번들러 도입 (Go 단일 바이너리 및 zero-dependency 원칙 준수)
* `live-semantic-map-final.html`에 존재하던 내부 텔레메트리(`67% Match`, `Invariant Proof: Guaranteed` 등 임의 추정치) 도입 (Anti-Telemetry 규칙 준수)
* 백엔드 API 스키마 변경 (이미 존재하는 `/api/task/view` 및 `/api/task/impact`의 현행 스키마 100% 재사용)

---

## 3. 액터 및 전제조건

* **Primary Actor**: 웹 브라우저를 통해 코드 실행 흐름 및 변경 파급 효과를 파악하려는 개발자
* **시스템 전제조건**:
  * 브라우저: 최신 표준 브라우저 (ES2020+, SVG 2.0 지원)
  * 백엔드 서버: CodeFlow Live Server (HTTP/SSE 제공)
* **데이터 전제조건**:
  * `/api/task/view`가 표준 `SemanticMapIR`, `flowContexts`, `semanticDelta`를 반환
  * `/api/task/impact`가 표준 `ChangeImpactGraph`를 반환

---

## 4. 아키텍처 및 모듈 설계

### 4.1 컴포넌트 계층 구조

```text
+-----------------------------------------------------------------------------------+
| HeaderBar (브랜드, 라이브 모드 배지, 정적 뷰 링크)                                |
+-----------------------------------------------------------------------------------+
| Intro & FlowQueryForm (흐름 요청 입력창 및 실행 버튼)                              |
+-----------------------------------------------------------------------------------+
| NoticeBar & Controls (상태 메시지, 이전 코드 비교, 읽기 고정, 변경 적용 버튼)     |
+-----------------------------------------------------------------------------------+
| ChangePulseLane (검증된 의미 변경 알약 목록)                                       |
+-----------------------------------------------------------------------------------+
| [신규] MacroStoryboardLane (상단 가로 스크롤 시퀀스 프레임 트랙)                   |
|  [Frame 1: UI] -> [Frame 2: Gate] -> [Frame 3: Domain Core] -> [Frame 4: App] ... |
+-----------------------------------------------------------------------------------+
| DualWorkbench Layout (3-Column Grid)                                              |
| +----------------+----------------------------------------+---------------------+ |
| | NavRail        | CenterViewport                         | TelemetryAside      | |
| | (좌측 순서 레일) | - ViewToolbar (코드 흐름 / 처리 흐름)    | - [신규] RadarWidget | |
| |                | - ConditionBar (조건 위치 필터)         |   (3중 동심원 SVG)  | |
| |                | - CodeFlowPanel / ProcessFlowPanel     | - ContextPanel      | |
| |                |   (선택 문장, 줄번호, 전후 비교 그리드) |   (호출자, 조건,    | |
| |                | - Boundaries (미확인 경계 안내)         |    상태 변경, 맥락) | |
| +----------------+----------------------------------------+---------------------+ |
+-----------------------------------------------------------------------------------+
| FooterBar (출처 및 소스 근거 안내)                                                |
+-----------------------------------------------------------------------------------+
```

### 4.2 자바스크립트 모듈 구조 (Single-File Modular Namespace)

외부 모듈 로더 없이 단일 파일 내에서 책임을 엄격히 격리하기 위해 `CodeFlow` 네임스페이스 패턴을 적용한다:

1. **`CodeFlow.Config` & `CodeFlow.Constants`**:
   * 아키텍처 계층 명칭 매핑 (`layers`), 엣지 명칭 (`edgeNames`), 변경 유형 명칭 (`deltaNames`)
2. **`CodeFlow.Store`**:
   * 반응형 단일 상태 스토어.
   * 상태: `data`, `baseline`, `selectedStepId`, `viewMode`('code'|'process'), `conditionFilter`, `paused`, `compare`, `impactGraph`, `isLoadingImpact`
   * 메서드: `getState()`, `subscribe(listener)`, `dispatch(action, payload)`
3. **`CodeFlow.API`**:
   * `/api/task/view`, `/api/task/impact`, `/api/flow/context`, `/api/workspace/stream` 비동기 호출 래퍼
4. **`CodeFlow.Components.Storyboard`**:
   * `render(steps, deltaChanges, selectedId)`: 수평 스크롤 트랙 생성
   * 프레임별 `ordinal`, `layer`, `name`, `technicalName`, `branch`, `sideEffect` 포맷팅
   * 변경된 단계 감지하여 `.surgery-badge` 및 `~ RULE CHG` 부여
5. **`CodeFlow.Components.Radar`**:
   * `render(targetSymbol, impactData, selectedStep)`:
     * 원점 `(0, 0)` 기준 `r=30`(1차 링), `r=62`(2차 링), `r=90`(3차 링) 렌더링
     * 1차 링: 직접 호출자(`directImpact.callers`) 및 상태 변경
     * 2차 링: 간접 호출자(`indirectImpact.callers`)
     * 3차 링: 테스트 노드(`directImpact.tests` / `indirectImpact.tests`)
     * 각도 분할 수식: $\theta_i = \frac{2\pi \cdot i}{N} - \frac{\pi}{2}$, 좌표: $x = r \cos \theta_i, y = r \sin \theta_i$
     * 노드 클릭 시 해당 심볼로 탐색 연동
6. **`CodeFlow.Components.CodeFlow` / `ProcessFlow`**:
   * 코드 카드 및 처리 카드 렌더링, 줄 번호, 선택 문장 하이라이트, 전후 비교 뷰
7. **`CodeFlow.Components.ContextPanel`**:
   * 선택된 단계의 상세 맥락, 선행 호출자 목록, 분기 조건, 외부 효과
8. **`CodeFlow.Bootstrap`**:
   * `// SAMPLE_BOOTSTRAP` 마커를 통해 라이브 제품 초기화(`boot()`)와 독립 샘플 초기화 분기

---

## 5. 비즈니스 규칙 및 불변식 (Invariants)

* **INV-01 (Single Selection Invariant)**: 스토리보드 프레임, 좌측 레일, 중앙 코드/처리 카드, 우측 레이더 중심 타겟은 언제나 동일한 `selectedStepId`를 가리켜야 한다.
* **INV-02 (Anti-Telemetry Invariant)**: 화면에는 컴파일러 내부 지표(에포크, 결제 지연 시간, 미확인 머신러닝 신뢰도 퍼센트 등)를 표시하지 않으며, 오직 검증된 소스 코드, 아키텍처 계층, 구체적 호출/테스트 관계만 표시한다.
* **INV-03 (Source Truth Invariant)**: 소스 코드가 없거나 분석이 실패한 구간은 가짜 코드를 생성하지 않고 `분석에서 확인한 다음 연결 없음` 또는 `미확인 경계`로 정직하게 표시한다.
* **INV-04 (Pipeline Parity Invariant)**: `scripts/generate_live_semantic.py`를 실행했을 때 `internal/flowview/live_view.html`이 온전히 생성되어야 하며, Go 테스트 `TestLiveViewMatchesDesignatedTemplate`를 항상 통과해야 한다.

---

## 6. 장애 및 경계 동작 (Failure & Boundary Behavior)

| 상황 | 프론트엔드 처리 방식 |
| :--- | :--- |
| **`/api/task/impact` 호출 실패 (네트워크/서버 오류)** | 레이더 영역에 오류 배너를 띄우지 않고, 로컬 `semanticMap.edges`의 직접 호출자 데이터를 기반으로 1차 링을 구성하며 "추가 파급 분석 미확인"으로 안전하게 폴백한다. |
| **테스트 관계(`test_covers`) 부재** | 3차 링을 억지로 채우지 않고, 빈 상태를 유지하며 "테스트 근거 미확인" 안내를 표시한다. |
| **단계 수가 많은 흐름 (10개 이상)** | 상단 스토리보드가 줄바꿈으로 화면을 밀어내지 않고, 가로 스크롤(`overflow-x: auto; scrollbar-gutter: stable`)로 매끄럽게 탐색되도록 처리한다. |
| **코드 편집 중 라이브 갱신 수신** | 사용자가 읽고 있는 위치(`readingPosition`)와 선택 상태(`selectedStepId`)를 보존하며, '읽기 고정(Paused)' 중에는 백그라운드 펜딩으로 전환한다. |

---

## 7. 기능 수용 기준 (Acceptance Criteria)

* **AC-01 (Clean Architecture)**: `live-semantic-map-prototype.html`이 압축된 단일 라인 스타일에서 벗어나, CSS/HTML/JS 모듈이 명확히 구조화되어 가독성과 유지보수성이 확보되어야 한다.
* **AC-02 (Storyboard Rendering & Sync)**:
  * 로드된 흐름의 단계들이 상단 스토리보드에 순서대로 렌더링되어야 한다.
  * 수정되거나 추가된 단계에 `~ RULE CHG`, `+ NEW SURGERY` 배지가 표시되어야 한다.
  * 스토리 카드를 클릭하면 중앙의 해당 코드 카드 및 좌측 레일이 즉시 활성화되고 스크롤 이동해야 한다.
* **AC-03 (Blast Radius Radar Rendering & Fallback)**:
  * 우측 상단에 3중 동심원 SVG 레이더가 렌더링되어야 한다.
  * 선택된 단계의 심볼이 레이더 중심에 위치하고, 호출자와 테스트 노드가 각각 1차/2차/3차 링에 적절한 각도로 분산 배치되어야 한다.
  * 샘플 모드에서 각 시나리오(원형, 재고 부족 조건 추가 등) 전환 시 레이더가 동적으로 반응해야 한다.
* **AC-04 (Build & Test Parity)**:
  * `python3 scripts/generate_live_semantic.py` 실행 시 정상 생성되어야 한다.
  * `go test ./internal/flowview -run TestLive`가 에러 없이 성공해야 한다.

---

## 8. 실행 계획 (Implementation Plan)

1. **Step 1: CSS 및 레이아웃 현대화**:
   * CSS Grid 기반 3단 워크벤치 및 상단 스토리보드 트랙 스타일 정의
   * SVG 레이더용 전용 스타일 및 노드 뱃지 스타일 정의
2. **Step 2: 자바스크립트 코어 모듈화 (`CodeFlow.*`)**:
   * `Store`, `API`, `Components` 네임스페이스 구조화
   * `Storyboard` 및 `Radar` 렌더링 함수 구현
   * 양방향 이벤트 디스패처 구현
3. **Step 3: 목데이터 보강 및 인터랙션 시연**:
   * `samplePayload()`에 레이더용 호출자/테스트 관계 데이터 추가
   * 샘플 시나리오 버튼(`sampleReset`, `sampleEdit` 등)과 레이더/스토리보드 반응성 연결
4. **Step 4: 빌드 파이프라인 및 테스트 검증**:
   * `python3 scripts/generate_live_semantic.py` 실행
   * `go test ./internal/flowview` 및 `make check-naming` 검증 완료
