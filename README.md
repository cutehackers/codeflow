# CodeFlow (v0.4.0)

> **대규모 코드베이스에서 비즈니스 핵심 흐름(Core Flow)을 엔드투엔드로 추출하고 시각화하는 다중 언어 개발자 및 AI 에이전트 도구**

CodeFlow는 진입 계층(UI 이벤트, API 요청 등)부터 컨트롤러, 유스케이스, 도메인, 데이터/외부 연동 계층까지의 **아키텍처 레이어 관통 경로(Core Flow)**를 검증된 코드 근거(Anchor)와 함께 추출하고, 인터랙티브한 **FlowView** 7-Lane 맵으로 시각화합니다.

---

## ⚡ 원샷 설치 (One-Shot Install)

> 💡 **AI 에이전트 / LLM 필독 지침**: AI 에이전트(Codex, Claude Desktop, Cursor, Antigravity 등)가 설치나 프로젝트 분석을 진행할 때는 반드시 **[`docs/guides/llm-usage.md`](docs/guides/llm-usage.md)**를 먼저 읽고 지침을 따르세요. 원샷 설치부터 아키텍처 자동 분석(`codeflow.layers.yaml` 생성) 및 핵심 흐름 추출까지의 상세 계약이 정의되어 있습니다.

터미널에서 아래 명령을 실행하면 Go/Dart/Node 컴파일러 없이도 OS/아키텍처에 맞는 사전 빌드 바이너리, 다중 언어 어댑터(Dart & TypeScript), 4대 AI 에이전트(Codex, Claude Desktop, Cursor, Antigravity) MCP 및 스킬까지 한 번에 자동 설치됩니다.

```sh
curl -fsSL https://raw.githubusercontent.com/cutehackers/codeflow/main/scripts/install.sh | bash
```

> 로컬 저장소 체크아웃 내에서는 `bash scripts/install.sh`를 실행하여 직접 빌드/설치할 수도 있습니다.

- **지원 언어**: Dart/Flutter, TypeScript/JavaScript (React, Node.js), Kotlin/Java, Swift, Python, Go, Rust
- **지원 에이전트**: Codex, Claude Desktop, Cursor IDE, Antigravity / Gemini CLI
- **설치 확인**: `$HOME/.local/bin/codeflow doctor <분석할-저장소-경로>`
- **지원 환경**: macOS (Apple Silicon / Intel), Linux (x86_64 / arm64)
- **원클릭 제거**: `$HOME/.local/bin/codeflow uninstall` (셸 rc나 프로젝트 코드를 일체 오염시키지 않습니다)

---

## 선택 기능: Semantic Enrichment

CodeFlow의 핵심 흐름 분석과 FlowView는 로컬 언어 모델(SLM) 없이도 정상 작동합니다. Semantic Enrichment는 검증된 분석 결과에 사람이 읽기 쉬운 의미 설명과 후보를 추가하는 선택 기능입니다.

현재 v0.4.0은 모델 실행 계층과 실패 시 기본 분석 결과를 유지하는 동작을 포함하지만, 사용자가 모델을 설치하고 연결하는 CLI는 아직 제공하지 않습니다. 따라서 별도 설정이 없다면 `Semantic Enrichment: unavailable`은 정상 상태입니다. 현재 지원 범위와 주의사항은 **[Semantic Enrichment 가이드](docs/guides/semantic-enrichment.md)**를 확인하세요.

---

## 🤖 에이전트 자율 설치 및 워크플로우 (Zero-Explanation Setup)

AI 에이전트(Codex, Claude Desktop, Cursor, Antigravity 등)는 **[`docs/guides/llm-usage.md`](docs/guides/llm-usage.md)**를 참조하여 **사용자의 추가 설명 없이도** 다음 단계를 순서대로 자율 실행합니다:

1. **원샷 설치 실행**:
   ```sh
   curl -fsSL https://raw.githubusercontent.com/cutehackers/codeflow/main/scripts/install.sh | bash
   # 또는 소스 체크아웃 내에서
   bash scripts/install.sh
   ```
2. **설치 정상 여부 점검**:
   ```sh
   $HOME/.local/bin/codeflow doctor <대상-저장소-경로>
   ```
3. **프로젝트 아키텍처 분석 및 레이어 자동 구성**:
   - 대상 프로젝트 구조(Feature-first, Layer-first, Clean, Hexagonal, FSD, MVC 등)를 파악하여 [`codeflow.layers.yaml`](docs/guides/llm-usage.md#01-프로젝트-아키텍처-분석-및-codeflowlayersyaml-작성-가이드) 자동 생성
4. **핵심 흐름(Core Flow) 추출 및 시각화**:
   - MCP 도구로 핵심 흐름을 추출·검증(`publish_core_flow`)하고 FlowView 리뷰 URL 제공

---

## 💬 핵심 프롬프트 예제 (Sample Prompts)

AI 에이전트에게 다음과 같이 자연어로 요청하세요:

### 1. 기존 기능의 전체 코드 흐름을 FlowView로 이해
```markdown
"$codeflow 이 프로젝트의 이메일 회원가입 기능이 시작부터 완료까지 어떤 코드 경로로 실행되는지 분석하고 화면으로 보여줘"
```

### 2. 코드 편집을 따라 Live Semantic Map 갱신
```markdown
"$codeflow 결제 처리 기능을 코드 수정과 함께 계속 분석해줘. 관련 코드를 수정할 때마다 최신 코드까지 검증됐는지, 아직 확인이 필요한 부분이 있는지 알려줘"
```

### 3. 변경 전후 의미와 영향 검토
```markdown
"$codeflow 이번 수정으로 사용자 동작이 어떻게 달라졌는지 비교하고, 요구사항 충족 여부와 영향을 받는 호출 코드·상태·외부 서비스·테스트를 근거와 함께 알려줘"
```

전체 기능과 상황별 프롬프트는 **[기능 및 프롬프트 가이드](docs/guides/feature.md)**를 확인하세요.

---

## 📚 관련 문서

- **프로젝트 개요 (Core Capabilities & Product Surfaces)**: [`docs/PROJECT.md`](docs/PROJECT.md) ([한국어](docs/PROJECT-ko.md))
- **아키텍처 및 유지보수 가이드**: [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md)
- **LLM / 에이전트 계약 가이드**: [`docs/guides/llm-usage.md`](docs/guides/llm-usage.md)
- **전체 기능 및 프롬프트 가이드**: [`docs/guides/feature.md`](docs/guides/feature.md)
- **선택적 Semantic Enrichment 가이드**: [`docs/guides/semantic-enrichment.md`](docs/guides/semantic-enrichment.md)
- **다중 언어 어댑터 프로토콜 사양**: [`docs/design/specs/llm-language-adapter-protocol.md`](docs/design/specs/llm-language-adapter-protocol.md)
- **다중 언어 마스터 플랜**: [`docs/design/specs/multi-language-foundation-plan.md`](docs/design/specs/multi-language-foundation-plan.md)
- **개발 환경 및 CLI 가이드**: [`docs/guides/development.md`](docs/guides/development.md)
