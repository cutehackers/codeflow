# Semantic Labeling 설정 및 사용 가이드

Semantic Labeling은 CodeFlow가 확인한 FlowSequence 관문에 사람이 읽기 쉬운 한 줄 의미 라벨을 제안하는 선택 기능입니다. 라벨은 검증된 Fact, Evidence, source anchor를 대체하지 않으며 항상 제안으로만 표시합니다.

SLM은 Go 코어에 포함하지 않습니다. CodeFlow는 개발자 환경의 OpenAI 호환 로컬 SLM 런타임과 HTTP로 통신합니다. SLM이 없어도 정적 FlowSequence와 코드 근거 탐색은 정상 작동합니다.

## 1. 설정

프로젝트 루트의 `codeflow.config.json`에 선택 설정을 추가합니다.

```json
{
  "slm": {
    "enabled": true,
    "endpoint": "http://localhost:11434/v1",
    "model": "qwen2.5-coder:1.5b",
    "timeoutMs": 500
  }
}
```

`enabled`의 기본값은 `false`입니다. `CODEFLOW_SLM_ENABLED=1` 또는 `true`로 명시적으로 활성화할 수 있으며, 환경 변수 값이 설정 파일보다 우선합니다.

## 2. HTTP 계약

FlowView는 `POST /api/semantic/labels`로 현재 FlowSequence의 라벨을 요청합니다.

```json
{
  "flowID": "checkout",
  "snapshotID": "snapshot-123",
  "frames": [
    {
      "frameID": "frame-01",
      "role": "entry",
      "title": "주문 요청",
      "stepRefs": ["step-01"],
      "primaryStepRef": "step-01"
    }
  ]
}
```

응답은 `requestID`, `flowID`, `snapshotID`, `status`, `labels[]`를 포함합니다. 각 라벨은 `frameID`, `text`, `status`를 갖습니다.

| 상태 | 의미 |
|---|---|
| `proposed` | 로컬 SLM이 반환한 제안 |
| `fallback` | SLM이 비활성화되었거나 사용할 수 없어 정적 제목을 유지 |
| `timed_out` | 설정된 시간 안에 응답하지 않아 정적 제목을 유지 |

응답 JSON이 잘못되었거나 프레임이 누락·중복되면 전체 응답을 `fallback`으로 처리합니다. SLM 출력으로 코드 사실, Evidence, source anchor를 변경하지 않습니다.

## 3. 실행 예시

```sh
FLOWVIEW_URL='http://127.0.0.1:4567'
FLOWVIEW_TOKEN='<FlowView가-출력한-token>'

curl -fsS -X POST \
  "$FLOWVIEW_URL/api/semantic/labels?token=$FLOWVIEW_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"flowID":"checkout","snapshotID":"snapshot-123","frames":[]}'
```

실제 요청의 `frames`에는 CodeFlow가 같은 snapshot에서 확인한 FlowSequence frame을 넣어야 합니다. 임의의 코드나 현재 워킹 트리의 다른 버전을 섞지 않습니다.

## 4. 안전 규칙

- 라벨 요청은 FlowSequence가 화면에 표시된 뒤 비동기로 실행합니다.
- 연결 실패와 malformed JSON은 화면 오류가 아니라 `fallback`으로 처리합니다.
- 타임아웃은 500ms 기본값이며 요청을 종료하고 `timed_out`을 반환합니다.
- 새 흐름 요청이나 frame 선택으로 이전 요청이 취소되면 늦게 도착한 응답을 store에 적용하지 않습니다.
- 로컬 런타임이 제공하는 라벨은 검토 가능한 제안이며 자동 승인하지 않습니다.
