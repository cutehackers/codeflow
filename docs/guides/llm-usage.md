# CodeFlow v0.4.0 LLM·에이전트 사용 계약

이 문서는 AI 에이전트가 CodeFlow v0.4.0을 사용할 때 따라야 하는 설치, 제거, 실행 및 설명 기준이다. CodeFlow가 반환한 코드 근거, 식별자와 검증 상태만 사실로 사용한다. 확인되지 않은 내용을 추론으로 채우지 않는다.

사용자가 실제로 요청할 수 있는 기능과 프롬프트 예시는 [기능 및 프롬프트 가이드](feature.md)를 참고한다.

---

## 0. 설치, 실행 전 확인 및 삭제 (Installation & Lifecycle)

### 0.1 원격 설치 (Remote One-Shot Installation)

```sh
curl -fsSL https://raw.githubusercontent.com/cutehackers/codeflow/main/scripts/install.sh | bash
```

설치 프로그램은 다음 자산을 사용자 환경에 배포하고 소유권 레코드(`$HOME/.codeflow/install-state.json`)를 생성합니다:
1. **CodeFlow Core 바이너리**: `$HOME/.local/bin/codeflow`
2. **다국어 AST 어댑터**: Dart 및 TypeScript 어댑터 풀
3. **에이전트별 MCP 자동 등록**: Codex, Claude Desktop, Cursor, Antigravity (`mcp_config.json`)
4. **CodeFlow 스킬 배포**: 에이전트 스킬 디렉터리에 `SKILL.md` 및 참조 문서(`references/`) 동기화

### 0.2 로컬 체크아웃 빌드 및 설치 (Local Build Installation)

소스 코드를 직접 클론한 환경에서는 다음 명령으로 로컬 빌드 및 설치를 수행합니다:

```sh
bash scripts/install.sh
```

### 0.3 환경 진단 및 실행 전 확인 (Preflight & Doctor)

설치 후 반드시 버전과 환경 무결성을 검증합니다:

```sh
# PATH에 $HOME/.local/bin이 등록되어 있지 않은 경우 추가
export PATH="$HOME/.local/bin:$PATH"

codeflow version
codeflow doctor <프로젝트-루트>
```

- **수동 MCP 설정 (Manual MCP Registration)**:
  자동 등록이 지원되지 않는 에이전트(Cline, Roo Code, VSCode 확장 등)나 커스텀 환경의 경우, 해당 에이전트의 MCP 설정 파일(`mcpSettings.json` 또는 `claude_desktop_config.json`)에 다음을 추가합니다:

  ```json
  {
    "mcpServers": {
      "codeflow": {
        "command": "$HOME/.local/bin/codeflow",
        "args": ["mcp"]
      }
    }
  }
  ```

- **언어별 런타임 요구사항**:
  - TypeScript/JavaScript 프로젝트 분석: Node.js 18+ 필요
  - Dart/Flutter 프로젝트 분석: Dart SDK 필요
  - Go 프로젝트 분석: Go 1.22+ (내장 AST 파서 기본 지원)
- **전역 색인(CodeGraph)**:
  - `.codegraph/` 디렉터리가 존재하는 경우 전역 정적 호출 그래프를 통해 진입점 탐색 및 레이더 시각화가 10ms 내로 고속화됩니다.
  - CodeGraph가 없거나 정지된 환경에서도 자체 내장 AST 어댑터 풀을 통해 100% 정상 작동합니다.
- 설치 후 에이전트 앱(Claude Desktop, Cursor, Antigravity 등)을 재시작하거나 새 세션을 열어 MCP 도구와 스킬을 새로고침합니다.

프로젝트가 아직 초기화되지 않았다면 작업 공간 구성을 초기화합니다:

```sh
codeflow init <프로젝트-루트>
```

### 0.4 안전한 완전 삭제 (Clean Uninstallation)

CodeFlow의 모든 자산과 에이전트 연동을 찌꺼기 없이 깨끗하게 제거하려면 다음 명령을 실행합니다:

```sh
$HOME/.local/bin/codeflow uninstall
```

- **삭제 보장 범위**:
  - `$HOME/.codeflow/install-state.json`에 기록된 설치기 소유 파일만 정밀 제거
  - 배포된 바이너리(`$HOME/.local/bin/codeflow`) 및 어댑터 제거
  - Codex, Claude Desktop, Cursor, Antigravity의 MCP 설정 파일에서 CodeFlow 항목만 선별 삭제
  - 설치기가 배포한 스킬 디렉터리(`skills/codeflow`) 삭제
- **사용자 수정 자산 보호**:
  - 설치 후 사용자가 직접 수정한 스킬 파일이나 다른 MCP 설정, 프로젝트 소스코드는 안전하게 보존됩니다.

---

## 1. 스토리 기반 비즈니스 관문과 1:N 실행 타임라인 아키텍처

CodeFlow는 획일적인 7계층 클린 아키텍처 강제에서 탈피하여, 실제 프로젝트 구조에 구애받지 않고 실행 흐름 상에서 일어나는 **비즈니스 사건(Story)** 중심의 6대 핵심 관문으로 흐름을 요약합니다.

### 1.1 거시적 FlowSequence vs 미시적 실행 타임라인

- **FlowSequence (거시적 의미)**:
  - 단위: 장면 (`FlowFrame`)
  - 분류: **6대 비즈니스 관문** (`entry`, `decision`, `process`, `effect`, `result`, `boundary`)
  - 수량: 전체 흐름당 **4~7개의 엄선된 핵심 카드**
  - 개발자 인지: *"시스템이 비즈니스 관점에서 무엇을 하려 하는가?"*
- **실행 타임라인 (미시적 코드 실행)**:
  - 단위: 구체적 코드 실행 단계 (`SemanticStep`)
  - 분류: 함수 호출(`call`), 유효성 가드(`guard`), 상태 변경(`mutation`), 결과 반환(`result`), 분석 경계(`boundary`)
  - 수량: 프레임당 1개 ~ N개 (아코디언 내부에 100% 보존)
  - 개발자 인지: *"해당 비즈니스 처리를 위해 실제 어떤 줄의 코드가 실행되는가?"*

### 1.2 2단계 백엔드 큐레이션 알고리즘

수백 개 단계의 실행 트레이스도 다음 2단계를 통해 직관적으로 큐레이션됩니다:
1. **1단계: 매크로 블록 응집 (Macro Clumping)**:
   - 연속된 if 가드문(`if err != nil` 등)을 단일 `decision` 관문("사전 유효성 검증")으로 결합
   - 연속된 상태 변경을 단일 `process` 관문으로 병합
   - 재귀 및 순환 호출(A $\to$ B $\to$ C $\to$ A)을 단일 대표 관문으로 자동 응집하고 `↺ RECURSION` 배지 표기
2. **2단계: 중요도 선별 (Significance Scoring)**:
   - $Entry(100) > Effect(90) > Process(85) > Decision(80) > 내부연산(50)$ 순으로 상위 4~7개 핵심 관문 선별
   - 하위 순위 단계는 직전 핵심 관문의 `stepRefs` 아코디언 내부로 흡수되어 전체 미시 라인을 보존

---

## 2. 공식 8대 핵심 MCP 도구 대응표

CodeFlow v0.4.0은 AI 에이전트의 토큰을 90% 이상 절감하면서도 정확한 비즈니스 흐름을 파악할 수 있도록 엄선된 **8대 핵심 MCP 도구**를 제공합니다:

| MCP 도구 명칭 | 핵심 역할 및 설명 |
|---|---|
| `harvest_flows` | 자연어 질의(`query`) 또는 도메인으로 후보 진입점을 탐색합니다. (흐름을 보려면 매칭된 심볼로 `analyze_flow`를 호출) |
| `analyze_flow` | 지정한 정확한 진입점 심볼(`entrySymbolPath`)을 정적 슬라이싱하여 FlowSpec 및 FlowSequence를 생성·발행합니다. |
| `get_flow_payload` | `flowId` 또는 `entrySymbolPath`를 전달하여 FlowSpec JSON 및 ~500토큰 고밀도 압축 페이로드(`CompactFlowPayload`)를 조회합니다. |
| `open_review` | 저장된 `viewId` 또는 `flowId`에 대응하는 인터랙티브 FlowView 브라우저 URL을 반환합니다. (사용자가 화면을 요청했을 때 브라우저에서 열도록 안내) |
| `publish_core_flow` | 에이전트가 직접 작성한 중간 아티팩트(`artifact`)의 앵커 무결성을 현재 작업 트리와 대조 검증한 후 핵심 흐름으로 발행합니다. |
| `submit_flow_draft` | 구조화된 E2 세션 여정 드래프트를 검증된 앵커와 함께 제출합니다. |
| `approve_step` | 특정 단계의 비즈니스 명칭과 규칙(`rules`)을 인플레이스로 승인(E3)합니다. |
| `report_unknowns` | 워크스페이스 내 확인되지 않은 호출 결손, 모호한 디스패치 및 분석 경계(`boundary`) 목록을 조회합니다. |

---

## 3. CodeGraph 연계 및 선택적 로컬 SLM 플러그인

### 3.1 CodeGraph 전역 정적 색인 연동

- **초고속 탐색**: `.codegraph/`가 있는 환경에서는 진입점 탐색(`harvest_flows`)과 1단계 직접 관계망(레이더)이 10ms 내로 즉각 렌더링됩니다.
- **다형성 및 인터페이스 해소**: Go 인터페이스나 TS/Dart 추상 클래스 호출 지점에서 구체 구현체 피호출자를 연결하여 분석 결손을 없앱니다.
- **Git 브랜치 전환 자동 바이패스 (Automatic AST Bypass)**:
  - 브랜치 전환(`git checkout/switch`) 시 색인 지연이나 DB 락이 감지되면 즉각 CodeFlow 내장 AST 슬라이서로 우회하여 최신 체크아웃 코드를 무중단 분석합니다.
- **미커밋 수정 실시간 재조정 (Re-anchoring)**:
  - 에디터 수정으로 심볼 라인/오프셋이 이동한 경우 SHA-256 스냅샷 검증을 거쳐 5ms 내에 올바른 코드 위치로 자동 재조정합니다. 심볼이 완전히 삭제된 경우에만 안전하게 `boundary` 관문으로 격리합니다.

### 3.2 선택적 로컬 경량 모델(SLM) 라벨러

- **역할 분담**: 로컬 런타임(Ollama 등, 예: `qwen2.5-coder:1.5b`)이 각 FlowSequence 관문의 한 줄 한국어 비즈니스 라벨을 비동기로 제안합니다.
- **무장애 폴백 (Fault Tolerance)**:
  - 500ms 타임아웃 강제 단절
  - 마크다운 코드블록 선제 제거 및 JSON 자동 파싱
  - 연결 실패 또는 파싱 오류 시 에러 팝업 없이 조용히 AST 정적 사실 텍스트 유지 (Silent Fallback)
  - 화면 이동 시 지연 도착한 과거 응답 자동 폐기 (Stale Response Discarding)

---

## 4. 단독 실행 가능 5대 모듈 및 CLI 사용법

CodeFlow는 웹 서버나 MCP 없이도 CLI 파이프라인으로 단독 실행할 수 있습니다:

```sh
# 1. analyzer: 프로젝트 언어 및 정적 호출 그래프 추출
codeflow analyze [프로젝트-루트]

# 2. collector: 특정 진입점 심볼로부터 원시 실행 경로 슬라이싱
codeflow collect <진입점-심볼>

# 3. curator: 원시 트레이스로부터 4~7개 비즈니스 관문 스토리보드 큐레이션
codeflow curate [trace.json | -]

# 4. presenter: 스토리보드 JSON을 브라우저 3열 워크벤치로 인터랙티브 서빙
codeflow view [flow-sequence.json | 프로젝트-루트]

# 5. agent-gateway: AI 에이전트 전용 stdio MCP 헤드리스 서버 구동
codeflow mcp [프로젝트-루트]
```

기존 편의 명령어 역시 그대로 지원됩니다:
- `codeflow init [path]`: 프로젝트 감지 및 `.codeflow/` 초기화
- `codeflow flows [path]`: 점수 순으로 후보 흐름 목록 출력
- `codeflow query [path] --mode feature --request "<질문>"`: 자연어 질의 기반 흐름 분석
- `codeflow status [path]`: 작업 공간 상태 및 스냅샷 확인
- `codeflow show <id|entry>`: 흐름 단계 및 비즈니스 규칙 조회
- `codeflow doctor [path]`: 환경 무결성 진단
- `codeflow uninstall`: 완전 삭제

---

## 5. 텔레메트리 은닉 및 개발자 코드 이해 최우선 원칙

- **Anti-Telemetry Guard**:
  - 컴파일러 에포크(`workspaceEpoch`), 지연 시간(`lag`), 내부 결재 락 플래그 등 엔진 내부 텔레메트리를 사용자 화면이나 주요 설명에 절대 노출하지 않습니다.
  - 오직 비즈니스 흐름 순회, 아키텍처 관문, 그리고 검증된 소스 코드 증거만을 명확하고 차분하게 제시합니다.
- **증거 기반 설명**:
  - 검증되지 않은 호출 관계를 추측으로 메우지 않고, 확인되지 않은 지점은 명직하게 `boundary` 또는 `unknown`으로 사용자에게 보고합니다.
