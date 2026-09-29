# CodeFlow — 프로젝트 개요 (Project Overview)

> **엔드투엔드 비즈니스 핵심 흐름 추출 및 스토리보드 시각화를 통한 대규모 코드베이스 이해 도구**

CodeFlow는 개발자와 AI 코딩 에이전트가 요청한 실행 흐름을 실제 코드로 읽도록 돕는 분석 도구입니다. 진입점에서 이어지는 문장과 관계를 소스 근거와 함께 보존하고 FlowView에 표시합니다. 검증된 단계에서 짧은 읽기 경로를 선별하더라도 전체 상세 단계는 유지합니다. 관문 수는 근거에 따라 달라지며, 요청에 맞는 요약을 확정하지 못하면 그 한계를 알립니다.

---

## 1. 핵심 역량 (Core Capabilities)

핵심 역량은 CodeFlow가 제공하는 도메인 엔진, 분석 방법론, 그리고 코드 분석의 신뢰성 및 정확도 보증 체계를 의미합니다.

### 1.1 엔드투엔드 핵심 흐름(Core Flow) 추출
* **계층 관통 실행 추적**: 진입 트리거(UI 클릭, HTTP 엔드포인트 요청, 라우트 또는 시스템 이벤트)에서 시작하여 컨트롤러, 유스케이스, 도메인 엔티티, 리포지토리/데이터, 그리고 인프라 및 외부 연동 API까지 이어지는 전체 실행 경로를 추적합니다.
* **요청 흐름 선택**: 코드 근거로 진입점을 고르고, 확인된 실행을 읽는 데 필요한 문장을 보존합니다. 실행이 입증되지 않은 콜백 본문은 구별하며 요약 과정에서 소스 단계를 삭제하지 않습니다.

### 1.2 무환각 코드 앵커 검증 (Zero-Hallucination Grounding)
* **검증된 코드 근거(Fact Anchors)**: 모든 실행 단계는 파일 상대 경로, 바이트 범위, 라인 번호, 파일 해시, 스팬 해시, 정규 AST 핑거프린트 등 실제 코드 팩트와 강력하게 결합됩니다.
* **미확인 영역(Unknowns)의 투명한 공개**: 동적 디스패치(Dynamic Dispatch), 미확정 타입, 외부 경계로 인해 정적 분석이 단절될 경우, 추측으로 메우지 않고 `unresolved_dynamic`, `unknowns[]`, 또는 `boundary` 관문으로 명확하게 보고합니다.

### 1.3 스토리보드 비즈니스 관문 및 비대칭 1:N 타임라인 모델
* **FlowSequence 관문**: 실행 흐름을 근거에 따라 `entry`, `decision`, `process`, `effect`, `result`, `boundary` 관문으로 묶습니다. 4~7개는 결과를 살피는 참고 범위이며 상한이나 요약 성공 기준이 아닙니다.
* **비대칭 1:N 계층적 포함 관계**: 거시적 비즈니스 의도(`FlowSequenceFrame`)와 미시적 코드 실행(`SemanticStep`)을 연결하여, 아코디언 내부에서 코드 라인 단위의 정확한 추적성을 보존합니다.
* **유연한 아키텍처 맥락**: 모든 프로젝트에 동일한 계층 구조를 강제하지 않으며, 아키텍처 분류는 각 장면에 부착되는 선택적 보조 라벨로 활용됩니다.

### 1.4 출처(Provenance) 및 신선도(Freshness) 수명주기 추적
* **신선도(Freshness) 보증**: 코드가 수정되었을 때 기존 분석 결과가 유효한지 추적합니다 (`fresh`: 최신 일치, `stale`: 코드 변경으로 재분석 필요, `orphaned`: 심볼 삭제됨).
* **출처(Provenance) 권위 체계**: 각 스텝 정보의 신뢰 수준을 계층적으로 관리합니다 (`approved` > `session` > `derived` > `unknown`).

### 1.5 비즈니스 의도 신호 마이닝 및 후보 점수화
* **진입점 후보 신호**: `derivedName`, `docLine`, `triggerClass` 등의 단서를 추출합니다. MCP `harvest_flows`의 질의는 문자열 필터이며, 요청에 맞는 후보인지는 실제 진입 코드를 확인해 판단합니다.

### 1.6 Track A "레이어 권위(Layer Authority)" 분리 원칙
* **구조적 AST와 아키텍처 매핑의 분리**: 언어 어댑터는 순수한 AST 구조적 팩트(`guard`, `mutation`, `call`, `effect`, `branch`)만 추출하며, 레이어 분류 및 비즈니스 흐름 큐레이션은 Go Core 큐레이터와 AI 에이전트가 전담합니다.

### 1.7 FlowView 코드 이해 대전제 (Code Comprehension Premises)
* **내비게이션 및 맥락 복원**: 사용자가 혼란을 겪지 않도록 관련 소스 코드와 주변 맥락을 직접 보여줍니다.
* **명시적 재분석 및 기준선 비교**: 현재 흐름 보기가 기본이며, 변경 전후 비교는 사용자가 명시적으로 선택한 기준선(Baseline)과 비교합니다. 백그라운드 파일 수정이나 watcher 이벤트가 화면을 임의로 바꾸거나 읽기 위치를 이동시키지 않습니다.
* **절제된 정보 공개 및 증거 중심**: 검증된 코드 읽기 경로를 앞에 표시하고 상세 실행 단계와 직접 관계는 필요할 때 엽니다. 기본 분석에서 요청별 관문을 가릴 근거가 부족하면 요약 한계를 표시합니다.
* **안티 텔레메트리 보호 (Anti-Telemetry Guard)**: 컴파일러 에포크, 지연 시간, 내부 결재 플래그, 색인 통계 등 내부 엔진 텔레메트리를 기본 화면에 절대 노출하지 않으며, 오직 검증된 비즈니스 흐름과 소스 코드만을 제공합니다.

---

## 2. 제품 접점 및 인터페이스 (Product Surfaces & Interfaces)

제품 접점은 사용자와 AI 에이전트가 CodeFlow의 핵심 역량을 실제로 활용할 수 있도록 제공되는 구체적인 소프트웨어 도구, 인터페이스 및 프로토콜입니다.

### 2.1 FlowView 인터랙티브 웹 UI
* **FlowView 워크벤치**: Svelte 5 화면에서 관문 탐색, 정확한 소스 강조, 펼칠 수 있는 상세 단계와 직접 관계 탐색을 제공합니다. 관문 수와 3열 배치를 고정하지 않습니다.
* **명시적 재분석 및 기준선 비교**: 사용자 명령에 따른 1회성 재분석 및 고정된 기준선과의 변경 전후 비교를 지원하며, 원치 않는 자동 화면 갱신을 배제합니다.
* **근거 기반 직접 관계망 레이더**: 선택된 장면에 대해 검증된 직접 호출자/피호출자, 상태 변경, 연관 테스트만을 탐색하며, 추측에 기반한 전역 영향 추정을 하지 않습니다.
* **토큰 인증 기반 로컬 보안**: 외부 노출 없이 `127.0.0.1` 루프백에만 바인딩되며, 실행 시 발급되는 일회용 암호화 토큰(`?token=...`)으로 보호됩니다.

### 2.2 멀티 에이전트 Model Context Protocol (MCP) 서버
* **Stdio JSON-RPC 인터페이스**: 4대 주요 AI 에이전트(**Codex, Claude Desktop, Cursor IDE, Antigravity / Gemini CLI**)를 위한 MCP 서버를 기본 내장합니다.
* **9개 MCP 도구**:
  * `publish_core_flow`: 코드 앵커를 검증하고 아키텍처 관통 핵심 흐름을 원자적으로 게시합니다.
  * `harvest_flows`: 진입점 후보를 탐색합니다. 선택적 `query`는 의미 검색이 아닌 문자열 필터입니다.
  * `get_flow_payload`: FlowSpec 또는 저장된 FlowView를 조회합니다. 압축 결과에도 단계와 관계 참조를 유지합니다.
  * `analyze_flow`: 임의의 진입점 심볼에 대해 온디맨드로 슬라이싱, 융합 및 게시를 수행합니다.
  * `submit_flow_draft`: 검증된 앵커가 포함된 구조화된 세션 여정 드래프트를 제출합니다.
  * `approve_step`: 단계 명칭 및 비즈니스 규칙을 사용자가 직접 승인합니다 (E3 승인 체계).
  * `report_unknowns`: 워크스페이스 내 확인되지 않은 호출 결손, 모호한 디스패치 및 분석 경계(`boundary`) 목록을 조회합니다.
  * `curate_code_reading_path`: 요청에 맞춰 선별한 단계를 소스·실행 관계·CodeView와 대조한 뒤 새 뷰로 저장합니다.
  * `open_review`: 필요 시점에 FlowView를 지연 구동하고 인증 토큰이 포함된 리뷰 URL을 반환합니다.
* **동적 타겟 라우팅**: 워킹 디렉터리를 오염시키지 않고 `target` 인자를 통해 특정 하위 디렉터리나 모노레포 패키지를 독립 분석합니다.

### 2.3 다중 언어 폴리글랏 엔진 및 프로세스 풀
* **격리된 서브프로세스 통신 (`stdio` NDJSON v1)**: 언어별 런타임과 Go Core가 줄바꿈 구분 JSON 스트리밍으로 독립 격리 실행됩니다.
* **언어 어댑터**:
  * **Dart / Flutter 어댑터**: Dart Analyzer SDK 기반의 심층 정적 분석 제공.
  * **TypeScript / JavaScript 어댑터**: 지원하는 문법을 parser로 분석합니다.
  * **Go 어댑터**: `go/ast`로 지원하는 문장과 직접 호출을 분석합니다.
* **프로세스 풀 관리자 (`AdapterRegistry`)**: 저장소 및 언어별로 재사용 가능한 격리된 서브프로세스 풀을 관리합니다.

### 2.4 선언적 아키텍처 설정 (`codeflow.layers.yaml`)
* **레이어 규칙 및 경로 매칭**: 디렉터리 글로브 패턴(`pathPatterns`) 및 관용적 별칭(`aliases`)을 표준 레이어에 매핑하는 YAML 규격입니다.
* **엄격성 제어**: 순서 위반(`strictOrder`) 및 미정의 레이어 허용 여부(`allowUnknownLayer`)를 제어합니다.

### 2.5 네이티브 CLI 툴체인
* `codeflow init [path]`: 대상 저장소를 초기화하고 `.codeflow/workspace.json`을 생성합니다.
* `codeflow flows [path]`: 점수순으로 정렬된 후보 흐름 목록을 출력합니다.
* `codeflow publish [path]`: 수집, 슬라이싱, 레이어 융합 및 게시를 일괄 수행합니다.
* `codeflow show <id|entry>`: 특정 흐름의 단계와 비즈니스 규칙을 JSON/텍스트로 출력합니다.
* `codeflow view` / `codeflow serve`: FlowView 웹 UI를 실행합니다.
* `codeflow mcp`: AI 에이전트용 MCP JSON-RPC 서버를 시작합니다.
* `codeflow doctor [path]`: 실행 환경, 도구 SDK, 어댑터 핀, 스키마 무결성을 진단합니다.
* `codeflow uninstall`: 설치된 MCP 설정, 스킬, 바이너리를 깨끗하게 제거합니다.

### 2.6 원샷 무마찰 인스톨러 (Zero-Friction One-Shot Installer)
* **단 한 줄의 원격 설치**: OS 및 CPU 아키텍처를 자동 감지하여 사전 빌드된 바이너리, 어댑터, 4대 에이전트 MCP 및 스킬을 셸 rc 오염 없이 한 번에 자동 설치합니다:
  ```sh
  curl -fsSL https://raw.githubusercontent.com/cutehackers/codeflow/main/scripts/install.sh | bash
  ```

### 2.7 단일 관문 비밀 정보 마스킹 (Single-Gate Secret Redaction)
* **중앙 집중식 자격증명 보호**: 모든 소스 코드 스팬, 설명문, FlowView 페이로드에서 API 키, 비밀 토큰, 비밀번호 등의 민감 정보를 사전에 정규식으로 자동 마스킹합니다.

---

## 3. 시스템 아키텍처 구성도

```mermaid
graph TD
    Client["AI 에이전트 / 사용자 / CLI"] -->|MCP / CLI| Core["CodeFlow Go Core"]
    
    subgraph Modules ["5대 독립 모듈 아키텍처 (internal/)"]
        direction TB
        M1["analyzer (detect, protocol, workspace, doctor)"]
        M2["collector (harvest, slicing, fusion, secret, storage)"]
        M3["curator (flow sequence, macro clumping, significance ranking)"]
        M4["presenter (flowview UI, workbench server)"]
        M5["agentgateway (mcp server, payload serializer)"]
        
        M1 --> M2
        M2 --> M3
        M3 --> M4
        M3 --> M5
    end
    
    M1 -->|stdio NDJSON v1| DartAdapter["adapters/dart (Dart SDK)"]
    M1 -->|stdio NDJSON v1| TSAdapter["adapters/typescript (Node.js 내장)"]
    M1 -->|stdio NDJSON v1| OtherAdapters["adapters/<lang> (다중 언어)"]
    
    M4 -->|HTTP 127.0.0.1 / Token Auth| Browser["인터랙티브 FlowView 3열 워크벤치"]
    M5 -->|stdio JSON-RPC| AIAgent["AI 코딩 에이전트 (MCP)"]
```

---

## 4. 관련 문서 색인

* **아키텍처 및 내부 구조**: [`docs/ARCHITECTURE.md`](ARCHITECTURE.md)
* **LLM 및 에이전트 지침**: [`docs/guides/llm-usage.md`](guides/llm-usage.md)
* **기능 및 프롬프트 가이드**: [`docs/guides/feature.md`](guides/feature.md)
* **개발 환경 및 CLI 가이드**: [`docs/guides/development.md`](guides/development.md)
* **다중 언어 어댑터 프로토콜**: [`docs/design/specs/llm-language-adapter-protocol.md`](design/specs/llm-language-adapter-protocol.md)
* **영문 프로젝트 개요**: [`docs/PROJECT.md`](PROJECT.md)
