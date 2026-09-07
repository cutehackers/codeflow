# CodeFlow Documentation

문서의 1차 분류는 디렉터리로 관리합니다. 파일명에는 문서의 주제와 필요한 경우 버전·날짜·범위만 표시합니다. 구현 티켓은 문서가 아니라 `.tasks/` 아래에서 관리합니다.

## Start here

- [Project overview](PROJECT.md) · [한국어](PROJECT-ko.md)
- [Architecture](architecture/architecture.md)
- [LLM and agent guide](guides/llm-usage.md)
- [Design specifications](design/specs/)
- [Contracts](contracts/)
- [Validation runbooks](validation/)
- [Versioning and release protocol](VERSIONING.md)

## Categories

| Directory | Purpose |
|---|---|
| `architecture/` | 현재 아키텍처 청사진과 구조 검토 |
| `contracts/` | 런타임·프로토콜·스키마 계약 |
| `design/specs/` | 구현 기준이 되는 설계 명세 |
| `design/decisions/` | 설계 결정 기록 |
| `design/raw/` | 아직 정리되지 않은 설계 초안과 원자료 |
| `guides/` | 개발자와 에이전트의 사용·개발 절차 |
| `releases/` | 릴리즈 준비와 인수인계 |
| `validation/` | 검증 실행 절차와 증적 수집 방법 |
| `samples/` | FlowView와 문서 예시 |

## Naming rules

- 파일명은 소문자 kebab-case를 사용합니다.
- 문서 종류는 디렉터리로 구분합니다. `VS10_`, `RELEASE_` 같은 분류용 prefix는 추가하지 않습니다.
- 버전이 계약의 의미를 바꿀 때만 `-v1`, `-v2`를 사용합니다.
- 날짜는 결정이나 시점별 명세처럼 시간 순서가 문서 식별에 필요할 때만 `YYYY-MM-DD-`로 시작합니다.
- `VS-10`처럼 범위를 나타내는 식별자는 파일명에 남기되, 문서 역할은 `validation/` 같은 디렉터리와 명확한 주제명으로 표현합니다.
- 문서의 상태와 정본 여부는 파일명에 넣지 않고 문서 본문에서 선언합니다.
