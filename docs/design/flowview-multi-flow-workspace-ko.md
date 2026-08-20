# FlowView Multi-flow Workspace 디자인 방향

상태: 구현 및 로컬 검증 완료
결정일: 2026-08-19
기준: 기존 단일 FlowView, `sgp-981-app` `/join`, 사용자 승인 샘플

## 1. 결정

FlowView의 상위 정보 구조는 `Multi-flow Workspace`로 확장한다. 여러 화면의
타임라인을 하나로 합치지 않고, 먼저 화면 사이의 이동을 보여 주는 **화면 흐름
지도**를 제공한 뒤 선택한 화면 하나의 상세 인과 흐름을 보여 준다.

상세 타임라인은 현재 FlowView의 흑백 감각을 유지하되 수직으로 재해석한다.
현재의 원형 마커, 얇은 인과 연결선, 선택 강조, 상태 변경의 이중 링, 분기 시
연결선 단절과 `또는` 표기를 그대로 보존한다.

## 2. 디자인 철학

이 방향은 TDS의 시각 스타일을 복제하지 않는다. 다음 설계 태도를 CodeFlow에
적용한다.

1. **다양한 사례를 패턴화한다.** 단일·복수 화면, 긴 텍스트, 분기, 상태 변화,
   unknown을 동일한 컴포넌트 계약으로 표현한다.
2. **기능만 맞는 기본 UI에서 멈추지 않는다.** 정보 밀도, 간격, 선택 피드백,
   연결선의 무게와 읽는 순서를 제품 수준으로 조정한다.
3. **모든 사용자의 사용성을 컴포넌트가 책임진다.** 키보드, 포커스, 큰 텍스트,
   좁은 화면, 스크린리더 상태를 화면마다 다시 결정하지 않는다.
4. **시스템은 통제가 아니라 문제 해결 도구다.** 하나의 고정 레이아웃을 강제하지
   않고 동일 primitive로 single-flow와 multi-flow 맥락을 지원한다.

## 3. 정보 구조

화면은 다음 순서로 읽힌다.

1. **공통 분석 기준** — repository, revision, worktree fingerprint, 분석한 flow 수
2. **화면 흐름 지도** — `/join → /home`, `/join → /auth` 같은 화면 간 관찰 관계
3. **선택한 화면 요약** — flow ID, 신뢰 상태, 한 문장 결과
4. **아키텍처 인과 지도** — 선택한 flow의 UI/application/state/data/external 경계
5. **상세 작업영역** — 수직 타임라인과 선택 단계의 코드·상태·화면 결과
6. **인지 부채** — 선택한 flow에 아직 연결되지 않은 동작만 표시

화면 흐름 지도는 탐색 수단이고, 수직 타임라인은 선택한 화면 내부의 인과 검토
수단이다. 두 계층을 한 그래프에 섞지 않는다.

## 4. 수직 타임라인 계약

- 원형 마커는 현재 step 순서와 trust 상태를 유지한다.
- 관찰된 단계는 채운 원, unknown은 점선 원, mixed는 굵은 원으로 구분한다.
- 상태를 실제로 변경하는 단계는 마커 바깥에 이중 링을 표시한다.
- 단계 사이의 얇은 수직선은 관찰된 순차 인과만 의미한다.
- 배타적 outcome 사이에는 연결선을 그리지 않는다.
- 분기 outcome은 `분기 N · 경로 A/B`와 `또는` 표기를 함께 사용한다.
- 선택된 단계는 전체 행을 과도하게 반전하지 않고, 현재의 흑백 마커와 선을
  유지한 채 텍스트 옆의 굵은 수직선과 약한 배경으로 강조한다.
- 이전/다음 버튼과 위치는 상세 카드 상단에서 단계 제목과 같은 레벨에 둔다.
- timeline, architecture node, branch outcome, 이전/다음은 하나의 selection state를
  공유한다.
- `VS Code에서 열기`는 상세 코드 근거의 핵심 동작으로 유지한다. 선택한 flow나
  step이 바뀌면 링크도 같은 selection state를 따라 정확한 파일·행으로 갱신한다.
- 링크는 현재 Basis의 manifest hash, anchor hash와 raw bytes가 모두 일치한 verified
  source lens에만 노출한다. stale 또는 unavailable 근거에는 비활성 링크를 남기지
  않고 열 수 없는 이유를 짧게 표시한다.

## 5. Multi-flow 데이터 계약

여러 flow를 함께 보여 주려면 다음 조건을 모두 만족해야 한다.

- 모든 flow는 같은 `Basis`의 repository, head revision, dirty state, manifest와
  worktree fingerprint로 컴파일된다.
- 한 번의 manifest capture와 하나의 Dart Analyzer 세션을 공유한다.
- 각 flow의 FlowIR은 독립적으로 검증하지만 batch publication은 원자적으로 한다.
- 일부 flow가 실패하면 성공한 flow와 다른 Basis의 결과를 섞지 않는다. 이전 batch를
  유지하고 실패 flow를 typed unavailable/unknown으로 보고한다.
- 화면 간 edge는 한 flow의 observed visible result와 다른 flow의 current entry point가
  모두 같은 Basis에서 검증될 때만 observed가 된다.
- flow 내부 step을 다른 flow timeline에 복사하지 않는다.

## 6. 반응형·접근성

- 데스크톱은 수직 타임라인과 상세 카드를 나란히 배치한다.
- 좁은 화면에서는 화면 흐름 지도, 타임라인, 상세 카드 순서로 한 열에 쌓는다.
- 타임라인은 큰 텍스트에서도 수평 스크롤에 의존하지 않는다.
- 모든 선택 항목은 native button이며 선택 상태를 `aria-pressed` 또는
  `aria-selected`로 노출한다.
- 선택 변경 시 상세 제목과 단계 위치를 함께 갱신하고, 포커스를 강제로 빼앗지
  않으면서 live region으로 결과를 알린다.
- 색만으로 trust, branch, state change를 구분하지 않는다.
- reduced motion에서는 스크롤·전환 애니메이션을 제거한다.

## 7. 성능 목표

정확도를 우선하며, 최초 multi-flow MVP는 최대 3개 flow를 동일 Basis에서 25초 안에
표시하는 것을 목표로 한다. 이를 위해 analyzer와 manifest를 공유하되 검증 단계를
생략하거나 stale snapshot을 결합하지 않는다. 목표 시간을 넘기면 진행 상태와 마지막
일관된 batch를 보여 주며 부분 결과를 ready로 표시하지 않는다.

## 8. 완료 조건

- 하나·둘·셋 이상의 flow가 같은 template과 primitive로 일관되게 렌더링된다.
- 화면 흐름 지도의 선택이 해당 flow의 architecture, timeline, detail로 이동한다.
- architecture, timeline, branch, previous/next 선택이 양방향으로 동기화된다.
- 선택한 단계의 verified source lens에서 `VS Code에서 열기`가 정확한 파일과 행을
  열며, stale·unavailable lens에는 링크가 생성되지 않는다.
- `/join`의 9단계, 두 분기, state change ring, `/home`·`/auth` 결과가 수직
  타임라인에서 인과적으로 오해 없이 표현된다.
- 320px, 736px, 1024px 및 큰 텍스트 환경에서 겹침·잘림이 없다.
- 키보드와 스크린리더로 flow와 step을 모두 탐색할 수 있다.
- 단일 FlowView API와 MCP의 기존 trust/evidence 의미는 바뀌지 않는다.

## 9. 비목표

배포, 컬러 테마 확장, 실행 trace 수집, product source 수정, 서로 다른 revision의
flow 병합은 이 디자인 목표에 포함하지 않는다.

## 10. 로컬 검증 결과

- 실제 `HOME/workspace/sgp-981-app`에서 `/join`, `/home`, `/auth`를 하나의
  Basis로 분석했으며 `/join → /home`, `/join → /auth`만 observed 화면 edge로
  게시했다.
- `make local`로 만든 AOT Dart adapter를 사용한 3-flow 분석은 16.26초로
  25초 MVP 목표 안에 완료됐다. 검증된 소스 합집합만 Analyzer context에 넣되
  flow별 evidence slice와 hash/range 검증은 유지한다.
- 실제 브라우저에서 320px, 736px, 1024px와 기본 데스크톱 폭을 검수했고 가로
  넘침 없이 수직 타임라인과 상세 카드가 배치됐다.
- timeline, Architecture node, branch, 이전/다음은 click과 Enter/Space 입력에서
  같은 selection state를 사용하며 선택된 source lens의 VS Code 링크가 함께
  변경된다.
- `.codeflow/cache`에는 reconstructable baseline mirror만 허용한다. 평상시에는
  3개, 동시 비교 보호 시간에도 최대 8개만 유지한다. 실제 대상의 현재 baseline cache는 비어 있었으며 AOT adapter는
  대상 저장소 cache가 아닌 CodeFlow의 ignored `libexec/`에 생성된다.
