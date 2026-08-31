# CodeFlow (v0.3.4)

> **대규모 코드베이스에서 비즈니스 핵심 흐름(Core Flow)을 엔드투엔드로 추출하고 시각화하는 다중 언어 개발자 및 AI 에이전트 도구**

CodeFlow는 진입 계층(UI 이벤트, API 요청 등)부터 컨트롤러, 유스케이스, 도메인, 데이터/외부 연동 계층까지의 **아키텍처 레이어 관통 경로(Core Flow)**를 검증된 코드 근거(Anchor)와 함께 추출하고, 인터랙티브한 **FlowView** 7-Lane 맵으로 시각화합니다.

---

## ⚡ 원샷 설치 (One-Shot Install)

> 💡 **AI 에이전트 / LLM 필독 지침**: AI 에이전트(Codex, Claude Desktop, Cursor, Antigravity 등)가 설치나 프로젝트 분석을 진행할 때는 반드시 **[`docs/llm-usage.md`](docs/llm-usage.md)**를 먼저 읽고 지침을 따르세요. 원샷 설치부터 아키텍처 자동 분석(`codeflow.layers.yaml` 생성) 및 핵심 흐름 추출까지의 상세 계약이 정의되어 있습니다.

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

## 🤖 에이전트 자율 설치 및 워크플로우 (Zero-Explanation Setup)

AI 에이전트(Codex, Claude Desktop, Cursor, Antigravity 등)는 **[`docs/llm-usage.md`](docs/llm-usage.md)**를 참조하여 **사용자의 추가 설명 없이도** 다음 단계를 순서대로 자율 실행합니다:

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
   - 대상 프로젝트 구조(Feature-first, Layer-first, Clean, Hexagonal, FSD, MVC 등)를 파악하여 [`codeflow.layers.yaml`](docs/llm-usage.md#01-프로젝트-아키텍처-분석-및-codeflowlayersyaml-작성-가이드) 자동 생성
4. **핵심 흐름(Core Flow) 추출 및 시각화**:
   - MCP 도구로 핵심 흐름을 추출·검증(`publish_core_flow`)하고 FlowView 리뷰 URL 제공

---

## 💬 핵심 프롬프트 예제 (Sample Prompts)

AI 에이전트에게 다음과 같이 자연어로 요청하세요:

### 1. 비즈니스 핵심 흐름 추출 및 FlowView 시각화
```markdown
"이메일 회원가입 핵심 흐름을 FlowView로 만들어줘"
"장바구니 담기부터 결제 완료까지의 엔드투엔드 레이어 관통 흐름을 시각화해줘"
```

### 2. 특정 진입점 및 상태 변화 / 외부 호출 분석
```markdown
"EmailSignupNotifier.submit 호출 시 발생하는 상태 변화와 외부 API 연동 흐름을 분석해줘"
"LoginView.handleSubmit 호출부터 UseCase, Repository까지의 슬라이싱 결과를 시각화해줘"
```

### 3. 프로젝트 아키텍처 분석 및 레이어 자동 구성
```markdown
"이 프로젝트의 아키텍처를 분석해서 codeflow.layers.yaml을 생성하고, 로그인 처리 핵심 흐름을 추출해줘"
```

---

## 📚 관련 문서

- **아키텍처 및 유지보수 가이드**: [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md)
- **LLM / 에이전트 계약 가이드**: [`docs/llm-usage.md`](docs/llm-usage.md)
- **다중 언어 어댑터 프로토콜 사양**: [`docs/spec/llm-language-adapter-protocol.md`](docs/spec/llm-language-adapter-protocol.md)
- **다중 언어 마스터 플랜**: [`docs/spec/multi-language-foundation-plan.md`](docs/spec/multi-language-foundation-plan.md)
- **로컬 CLI 사용법**: [`docs/local-usage.md`](docs/local-usage.md)
