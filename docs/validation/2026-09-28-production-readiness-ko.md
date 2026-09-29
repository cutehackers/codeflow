# CodeFlow 출시 검증 현황

- 기준일: 2026-09-28
- 기준 계약: [요청 흐름 코드 읽기 통합 계약](../design/specs/2026-09-23-requested-flow-code-reading-delivery-ko.md), [Requested Flow 관문과 실행 타임라인](../design/specs/2026-09-18-flowsequence-requested-flow-hierarchy-ko.md)
- 판정: 출시 준비 상태를 확정하지 않았다. 아래의 사용자 과제와 실제 릴리스 실행 증거가 필요하다.

| 검증 범위 | 현재 증거 | 남은 확인 |
| --- | --- | --- |
| 요청에서 실제 코드까지 | Dart Molycard 요청의 390개 단계와 431개 확인·미확인 관계를 분석했다. 저장 FlowView의 312개 관문에서 390개 단계를 각각 선택했으며 모든 코드 강조가 저장된 `exact/verified` 소스 범위와 일치했다. 짧은 읽기 경로에서는 상위 18개 코드 지점과 중간 상세 88개를 각각 열어 일치시켰다. | 이 사례의 선택 경로에서 제외된 조건 결과 24개는 화면에 한계로 알린다. 이 한 경로로 모든 Dart 문법과 분기의 정확성을 주장하지 않는다. |
| 세 언어의 설치본 | 임시 설치 환경의 macOS ARM64 아카이브와 Linux AMD64 컨테이너 아카이브에서 Dart·Go·TypeScript adapter 프로토콜을 확인했다. 두 플랫폼의 실제 요청에서 Dart 390개, Go 4개, TypeScript 7개의 코드 근거가 모두 `exact/verified`였고, 단계·관계·코드 근거·미확인 경계가 플랫폼 간 일치했다. macOS 설치본 화면에서 세 언어의 모든 단계를 선택해 코드 강조를 확인했다. Linux 설치본은 FlowView HTML과 Dart 저장 결과를 HTTP로 제공했다. | 공개 GitHub 릴리스에서 받은 아카이브와 태그 기반 CI 실행은 확인하지 않았다. |
| 실패 시 설치 보호 | 릴리스 아카이브의 필수 파일과 adapter 프로토콜을 복사 전에 확인한다. Dart adapter 누락과 불일치를 재현한 검사에서 설치 파일·MCP 등록·성공 기록을 남기지 않았다. | 실제 릴리스 실행에서 이 검사가 통과하는지 확인한다. |
| 자동 검증 | 현재 작업 트리에서 `make fmt`, `make vet`, `make check-naming`, `make test`, `git diff --check`, `make build`, `make test-ui`(59개), `make test-flowview`(45개), Dart `dart test`(120개), TypeScript `npm test`, `go test ./adapters/go/...`가 통과했다. 저장·MCP 영향 패키지의 `go test -race ./internal/presenter/flowview ./internal/agentgateway/mcp`도 통과했으며 이 검사에는 과거 View의 HTTP·MCP 읽기 및 저장 원본 불변성 사례가 포함된다. 릴리스 CI는 업로드 전 아카이브 체크섬과 세 adapter의 프로토콜을 검사하도록 설정했다. | 태그 기반 CI의 실제 결과를 확인한다. |
| 대규모 입력 반응 | 소스 파일만 둔 Molycard 임시 저장소에서 컴파일된 Dart adapter로 분석하는 데 약 2.3~2.6초가 걸렸다. 저장 화면의 18개 주요 항목 선택은 데스크톱에서 중간값 24ms, 최대 58ms였고 390px 화면에서 문서의 가로 넘침은 없었다. 단일 로컬 환경의 관측값이다. | [OPEN-UX-02](../design/specs/2026-09-18-flowsequence-requested-flow-hierarchy-ko.md)의 시험 환경, 합격 수치와 지원 사례 범위를 결정하고 그 조건에서 재측정한다. |
| 실제 개발자 이해 | [§16.59](../design/specs/2026-09-18-flowsequence-requested-flow-hierarchy-ko.md)의 네 과제에 맞춘 [평가 기록지](2026-09-28-flowview-developer-study-ko.md)를 만들었다. 준비 스크립트의 `--check` 실행으로 네 실제 API 분석과 전후 비교 기준의 저장을 확인했다. 같은 저장 분석을 사용하는 데스크톱 2열 `studyLayout=code-focused` 비교안을 준비했다. 준비 스크립트가 만든 네 저장 화면을 두 배치에서 각각 열어 코드 표시를 확인했고, 다른 파일 코드 선택·전후 코드 비교·미확인 콜백 코드 표시를 조작해 확인했다. | 실제 개발자가 두 배치에서 조건·공통 처리, 다른 파일 코드와 복귀, 전후 비교, 미확인 연결을 설명하도록 하고 정답·근거 도달·복귀·시간·오해를 기록한다. 에이전트의 화면 조작으로 대체하지 않는다. |

## 통합 계약의 구현 근거

아래 근거는 FA-01~08의 **현재 지원 사례**에 대한 것이다. 단일 사례나 검사 통과를 모든 문법·프로젝트의 지원으로 확대하지 않는다. 주요 검사 파일은 [adapter 공통 근거 검사](../../internal/analyzer/protocol/adapter_slice_conformance_test.go), [실제 저장 FlowView 브라우저 과제](../../web/live-comprehension-workspace/tests/saved-flowview.spec.ts), [HTTP·MCP 왕복 검사](../../internal/agentgateway/mcp/flow_payload_test.go)다.

| 기준 | 확인한 구현 경로와 근거 | 판정 범위 |
| --- | --- | --- |
| FA-01·02 | Dart adapter 검사와 Molycard의 390개 실제 문장·코드 범위, 브라우저의 조건·반복·예외·비동기 관계 과제 | 확인된 직접 관계와 표시된 미확인 경계에 한정 |
| FA-03 | Molycard의 모든 기본·상세 단계 코드 강조, Go·TypeScript 저장 화면 선택, 읽을 수 없는 대표 코드의 저장 거절 과제 | 확인한 요청과 snapshot에 한정 |
| FA-04 | 18개 주요 코드 지점과 중간 상세 88개 선택, 상세 보존 검사, 저장 FlowView와 MCP compact의 계층 대조 | 선택하지 않은 분기 24개는 화면에 알린 한계 |
| FA-05 | 실제 Dart·Go·TypeScript `slice`를 부르는 공통 검사와 언어별 기대·금지 관계, 해시·참조 실패 검사 | 문법별 의미는 각 언어 사례로 검증 |
| FA-06 | Go receiver·동명 메서드·오류 반환 검사, 설치본 Go 요청의 기본·상세 코드 선택 | 해당 Go 소스 형태에 한정 |
| FA-07 | TypeScript 함수·TSX 이벤트·조건·비동기 실패 검사, 설치본 TypeScript 요청의 기본·상세 코드 선택 | 해당 TypeScript·TSX 소스 형태에 한정 |
| FA-08 | 모호한 후보 선택 전 저장 중단, 읽을 수 없는 코드 거절, 실패·취소 시 이전 View·선택·비교 보존 과제 | 재현한 실패 유형에 한정 |

출시 판정에는 사용자 과제 결과, 지원 범위와 성능 기준, 실제 태그 기반 배포 결과를 함께 사용한다. 이 문서의 자동 검사와 로컬 설치 결과만으로 production-ready를 선언하지 않는다.
