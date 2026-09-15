# FlowView 프로젝트 적응형 Lane 스펙

- Contract ID: `FLOWVIEW-ADAPTIVE-LANES`
- Contract Status: Proposed
- Parent: `FLOWVIEW-CODE-COMPREHENSION` (Approved, `docs/design/specs/2026-09-14-flowview-code-comprehension-ko.md`)
- Created: 2026-09-14
- Glossary: `docs/design/glossary.md`의 Requested Flow 정의를 따른다
- Implementation Status: Specification only. 구현·배포·사용자 가치 검증 완료가 아니다.

## 1. 문제와 목표

고정 7-lane 표시는 모든 프로젝트에 맞지 않는다. Flutter 키워드(`controller`, `notifier`, `bloc`, `page`)에 고정한 추론은 Next.js, Go, Kotlin 프로젝트에서 오분류하거나 `application`/`unknown`으로 뭉친다.

목표는 저장용 정규화와 화면용 표시를 분리하는 것이다. 정규화는 canonical 7-layer를 유지하고, 화면은 사용자가 요청한 프로젝트에서 관측된 lane만 프로젝트 용어로 보여준다.

| Intent | Goal | 관찰 가능한 결과 |
|---|---|---|
| INT-FLOW-01 현재 구현을 이해한다 | GOAL-LANE-01 | 요청 흐름에 등장한 lane만 순서대로 표시한다 |
| INT-FLOW-01 | GOAL-LANE-02 | lane 이름이 프로젝트 폴더·심볼 용어와 일치한다 |
| GOAL-FLOW-02 부분 결과 구분 | GOAL-LANE-03 | 근거 없는 lane 추측을 `uncertain`으로 표시한다 |

## 2. 범위

포함 범위: `internal/flowview/lanes.go`의 `classifyNode`·`inferLanes`·`lanePlanFrom`·`decorateFromGraph` 증거 우선순위 변경, `codeflow.layers.yaml` 연결, 설정 없을 때의 자동 fallback, Requested Flow 기준 lane 필터.

제외 범위: canonical 7-layer 정의 변경, 저장 스키마·공개 protocol 필드 변경, 별도 Live 화면·watcher 자동 갱신·실시간 목표, 전체 저장소 영향 자동 추정, 무제한 lane 생성.

이 스펙은 기존 공개 스키마 이름·필드·`cas:sha256:<hex>`·URL·호환 fixture를 바꾸지 않는다. 새 동작은 명시적 capability 없이 기존 read-time decoration 경로 안에서 additive로만 제공한다.

## 3. 현재 구조와 결함

- `internal/fusion/layers.go`: `LayersConfig`(`version`, `layers[].name/aliases/pathPatterns`, `strictOrder`, `allowUnknownLayer`)와 `NormalizeLayer`, `ValidatePathPatterns`, 내장 `builtinAliasToCanonical`을 제공한다. 설정 파일이 있으면 파일이 source of truth다.
- `internal/flowview/lanes.go:10-47`: 적응형 v2 엔진 증거 우선순위(수동 override 1.00, side effect 0.90, 키워드 0.80, entry 0.75, 이웃 투표 0.65, 구조 중간 0.55, 기본 0.50)를 정의한다. `lanePlanFrom`은 관측된 lane만 canonical 순서로 표시한다. 이 부분은 유지한다.
- `internal/flowview/lanes.go:218-259 classifyNode`: `codeflow.layers.yaml`을 읽지 않는다. `InferLayer` 결과가 `application`일 때만 entry·투표로 넘어간다.
- `internal/flowview/layers.go:70-119 InferLayer`: 심볼 키워드(`controller`, `usecase`, `notifier`, `bloc`, `page`, `repository`)와 일부 경로 문자열을 하드코딩한다. 프로젝트별 `pathPatterns`·`aliases`를 반영하지 않는다.
- `internal/flowview/lanes.go:432-478 applyLayersWith`, `606-653 decorateAll`: 명시적 `layer`가 있으면 보존하고, 없으면 공유 그래프 plan을 그대로 쓴다. 요청 흐름 필터가 없다.

결함은 한 가지다. fusion은 프로젝트 설정을 존중하는데 flowview 표시는 그렇지 않다.

## 4. 변경 설계

### 4.1 증거 우선순위 변경

`classifyNode` 우선순위를 다음과 같이 바꾼다. 가중치는 기존 정수 백분율 체계를 유지한다.

1. 수동 laneOverride: 1.00, 유지.
2. 관측된 side effect: `external`, 0.90, 유지.
3. `codeflow.layers.yaml` 매칭: `pathPatterns` 매칭 또는 `aliases`·`name` 매칭. 0.80. snapshot 기반 분석에서는 스냅샷에 포함된 설정 bytes만 사용하고 live filesystem을 다시 읽지 않는다.
4. 기존 강한 키워드·경로 매칭(`externalSymbolKeywords`, `dataPathSegments`, `InferLayer`의 비-`application` 결과): 0.80, 단 3번과 충돌하면 3번이 이긴다.
5. entry seed: `page`, 0.75, 유지.
6. 이웃 다수 투표: 0.65, 유지. 투표 대상 layer는 3번 결과도 포함한다.
7. 구조 중간: `usecase`, 0.55, 유지.
8. 기본: `usecase`, 0.50, `uncertain=true`, 유지.

`InferLayer` 하드코딩은 4번 fallback으로 강등한다. 삭제하지 않는다. 설정이 없는 저장소의 회귀를 막기 위해서다.

### 4.2 설정 연결 규칙 (O1)

- 분석 입력에 `codeflow.layers.yaml`이 있으면 `LoadLayersConfigFromBytes`로 파싱한다. 파싱 실패는 `layers_config_invalid`로 보고하고 lane 추론을 중단하지 않는다. 해당 심볼은 `uncertain`으로 표시한다.
- `pathPatterns`는 `internal/fusion/layers.go:426 matchDoublestar`와 같은 매처를 사용한다. 새 매처를 복제하지 않는다.
- `aliases`·`name`은 소문자·trim·마지막 `/` segment 규칙(`NormalizeLayer`)을 따른다.
- 설정에 없는 단어는 기존 내장 별칭표로 해석하지 않는다. 설정이 있으면 설정이 source of truth다.

### 4.3 자동 fallback (O2)

설정이 없거나 매칭되지 않은 심볼에만 적용한다.

- 같은 저장소 안에서 디렉터리 segment 빈도와 entry 분포로 lane 후보를 만든다. 예: `routes/`, `pages/`, `components/`가 entry에 집중되면 `presentation` 후보.
- 후보는 `classifyNode` 4번과 같은 0.80이 아니라 투표(0.65) 입력으로만 사용한다. 자동 추측이 명시적 설정을 덮지 않게 한다.
- 후보 근거는 lane label이 아니라 `uncertain` 사유로 기록한다.

### 4.4 Requested Flow lane 필터 (O3)

- 단일 흐름 화면: 해당 흐름의 step에 등장한 canonical layer만 `lanes` 배열에 담는다. 순서는 `CanonicalLayerOrder`를 따른다.
- Architecture Map(`buildArchitectureMapWithSources`): 기존처럼 전체 관측 lane을 유지한다. Map과 단일 흐름의 lane 집합이 다를 수 있음을 명시한다.
- 선택 step이 삭제되거나 대응이 불명확하면 선택을 임의 항목으로 옮기지 않는다. 부모 계약 §5.2 규칙을 따른다.

### 4.5 lane 라벨

`laneLabel`의 최빈 convention 규칙을 유지한다. 설정 `aliases`에서 온 매칭은 convention으로 기록해서 lane 이름에 반영한다. 예: 프로젝트가 `handler`를 `usecase` 별칭으로 선언하면 lane은 `UseCase` 계열로 표시한다.

## 5. 데이터와 경계

| 논리 결과 | 필수 내용과 불변식 |
|---|---|
| Lane 추론 입력 | analysis snapshot ID, snapshot 내 설정 bytes identity, `overrides`, flow 문서 집합 |
| Lane 추론 출력 | step별 `layer`·`layerConfidence`·`layerUncertain`, `lanes[{id,label}]`. 필드 추가 없이 기존 decoration 키만 사용 |
| Architecture Map | `MapLane{id,label}`, `MapComponent{layer,confidence,uncertain}`, `MapRelation` 구조 유지. layer 값은 canonical ID만 사용 |

저장된 FlowSpec을 직접 수정하지 않는다. `applyLayersWith`·`decorateFromGraph`의 read-time decoration 계약을 유지한다. 명시적 `layer`는 보존한다.

## 6. 실패와 정직성

| 실패 | 결과와 보존 |
|---|---|
| 설정 파일 없음 | 내장 규칙+fallback으로 동작, 해당 lane은 `uncertain` 가능 표시 |
| 설정 파싱 실패 | `layers_config_invalid` 기록, 추론은 내장 규칙으로 계속, 실패를 미분류 확정으로 표시하지 않음 |
| 경로·별칭 충돌 | `pathPatterns`가 별칭보다 우선, 동점자는 정렬된 결정 순서로 해결 |
| 투표 미달·이웃 부족 | `usecase`+`uncertain`, 근거 없는 lane 단정 금지 |
| `unknown_edge` | 그래프 투표·관계 집계에서 제외, 구조 날조 금지 |

내부 epoch·lag·settlement 같은 내부 상태는 기본 화면에 노출하지 않는다.

## 7. 수용 기준

| ID | 상황 | 반드시 관찰할 결과 |
|---|---|---|
| FA-LANE-01 | `codeflow.layers.yaml`에 `handler→usecase` 별칭이 있는 저장소 | `handler` 심볼이 `usecase` lane에 0.80으로 배치되고 lane 이름에 반영됨 |
| FA-LANE-02 | 설정 없는 Next.js 저장소 | 하드코딩 Flutter lane(`상태 변경(Bloc)` 등)이 뜨지 않고, 관측 lane만 표시되며 근거 약한 lane은 `uncertain` |
| FA-LANE-03 | 설정 파싱 실패 | 기존 화면 유지 관점에서 lane은 내장 규칙 결과+실패 사유 표시, 미구현 단정 없음 |
| FA-LANE-04 | 단일 Requested Flow 조회 | 그 흐름에 없는 lane이 `lanes` 배열에 포함되지 않음 |
| FA-LANE-05 | 명시적 `layer`가 있는 core flow | 기존 layer 보존, lane은 해당 집합에서만 도출 |
| FA-LANE-06 | 기존 strict schema 소비자 | payload 필드·의미 변경 없음, 새 lane은 기존 `id` 값 범위 내 |

## 8. 검증 순서

1. `make fmt`, `make vet`, `make check-naming` 통과. 새 식별자에 약어·티켓 ID를 쓰지 않는다.
2. `go test ./internal/flowview ./internal/fusion` 통과. 기존 `eval_test.go`, `layers_test.go` 회귀 확인.
3. fixture 3종(Flutter 기존 저장소, Next.js 저장소, 설정 파싱 실패 저장소)으로 FA-LANE-01–06을 수동 확인. 각 FA에 입력, expected/actual, command, source revision을 기록한다.
4. 부모 계약 FA-02(계층 간 연결과 실제 source 맥락)와 FA-13(기존 소비자 호환)이 깨지지 않았는지 확인한다.

## 9. 결정과 미결정

결정1: canonical 7-layer는 저장·검증용으로 유지하고 표시는 적응형 projection으로 분리한다. 결정2: 설정 매칭을 키워드 하드코딩보다 우선한다. 결정3: 단일 흐름 화면은 요청 흐름 lane만 보여주고 Map은 전체 관측 lane을 유지한다.

미결정: 디렉터리 빈도 fallback의 임계값(최소 파일 수·비율)은 구현 단계에서 corpus로 고정한다. 수치를 발명하지 않는다.

완료는 §7 수용 기준과 §8 검증 통과, 같은 snapshot의 소스와 근거 보존, 공개 호환성 유지, 한계의 명시적 표시다. 이 문서의 Proposed는 스펙 초안이며 코드 구현·효과 입증·배포 승인이 아니다.
