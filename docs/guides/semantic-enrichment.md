# Semantic Enrichment 설정 및 사용 가이드

## 먼저 확인할 내용

Semantic Enrichment는 CodeFlow가 코드에서 검증한 핵심 흐름에 사람이 읽기 쉬운 제목, 분류와 설명 후보를 추가하는 선택 기능입니다. SLM의 제안은 코드 근거를 대체하지 않으며 검증을 통과한 경우에만 별도 보강 정보로 사용됩니다.

현재 v0.4.0의 배포용 `codeflow` CLI에는 SLM 설치 및 설정 명령이 없습니다. 따라서 일반 설치 사용자는 아직 SLM을 연결할 수 없습니다. 이 문서의 설정 절차는 **CodeFlow 소스에서 호환 Model Host를 직접 연결하는 개발자용 절차**입니다.
추후 이와 관련된 기능 제공이 있을 예정이지만 현재까지는 직접 연결을 제안합니다.

SLM이 없어도 핵심 흐름 탐색, 발행, FlowView, 변경 비교와 `current` 또는 `gap` 판정은 정상 작동합니다.

## 1. 준비물

현재 Model Host의 격리와 자원 제한 구현은 **macOS에서 CGO를 활성화한 빌드**만 지원합니다. Linux 또는 `CGO_ENABLED=0` 빌드에서는 Model Host 실행이 `unsupported`로 거부됩니다.

다음 두 파일을 분석 대상 저장소 밖에 준비합니다.

```text
$HOME/.local/libexec/codeflow/model-host
$HOME/.local/share/codeflow/models/<model>.gguf
```

- `model-host`는 CodeFlow Model Host Protocol v2를 구현한 실행 파일이어야 합니다.
- 모델 파일은 Model Host가 지원하는 GGUF 모델이어야 합니다.
- 두 파일 모두 분석 대상 저장소 안에 두면 안 됩니다.
- 원본 `llama-server`는 CodeFlow의 stdio NDJSON 계약을 구현하지 않으므로 직접 연결할 수 없습니다. `llama.cpp`를 사용하는 경우에도 두 계약을 변환하는 호환 Model Host가 필요합니다.

현재 CodeFlow 저장소와 설치 프로그램은 호환 Model Host 실행 파일이나 GGUF 모델을 배포하지 않습니다. 이 준비물이 없다면 아래 설정을 진행할 수 없습니다.

## 2. 설계에서 선택한 모델

설계에서는 `Qwen3-4B-Instruct-2507`을 초기 기본 모델로 선택하고 `Granite 4.2 3B`를 고정 비교 모델로 지정했습니다. 목표 양자화 형식은 `GGUF Q4_K_M`입니다.

| 설계 역할 | 모델 | 선택 목적 | 라이선스 | 현재 검증 상태 |
|---|---|---|---|---|
| 초기 기본 모델 | [Qwen3-4B-Instruct-2507](https://huggingface.co/Qwen/Qwen3-4B-Instruct-2507) | non-thinking 기반의 안정적인 지시 수행, 코드·다국어·한국어 설명 | Apache-2.0 | 설계에서 선택됨. 실제 CodeFlow 품질·지연시간 gate 검증 전 |
| 고정 비교 모델 | [Granite 4.2 3B](https://huggingface.co/ibm-granite/granite-4.2-3b) · [공식 GGUF](https://huggingface.co/ibm-granite/granite-4.2-3b-GGUF) | 더 작은 모델의 한국어 품질, 지연시간과 메모리 비교 | Apache-2.0 | 설계에서 challenger로 선택됨. 실제 CodeFlow gate 검증 전 |
| 고품질 비교 모델 | [Qwen2.5-Coder-7B-Instruct](https://huggingface.co/Qwen/Qwen2.5-Coder-7B-Instruct) | 4B 모델의 의미 압축 품질이 부족할 때 코드 중심 품질 비교 | Apache-2.0 | 메모리 여유 장비용 후보. 실제 CodeFlow gate 검증 전 |

첫 평가는 `Qwen3-4B-Instruct-2507 GGUF Q4_K_M`으로 진행합니다. 동일한 Evidence Pack, gold set과 장비에서 `Granite 4.2 3B GGUF Q4_K_M`을 비교합니다. 두 모델이 품질 기준을 충족하지 못하고 메모리 여유가 있을 때만 `Qwen2.5-Coder-7B-Instruct GGUF Q4_K_M`을 평가합니다.

다른 설계 초안에 나온 `Qwen2.5-Coder-1.5B-Instruct`, `Qwen3-4B`, `Phi-4-mini-instruct`는 탐색 후보입니다. 현재 가이드의 초기 기본 모델 결정을 대체하지 않습니다.

`Qwen2.5-Coder-3B-Instruct`는 Qwen Research License가 적용되므로 기본 배포 후보로 권장하지 않습니다. 특히 상업용 기본 모델 후보에서 제외합니다.

### 검증 상태를 정확히 이해하기

현재 자동 테스트는 `fake-model`, `mcp-test-model`, `flowview-test-model` 같은 결정적 테스트 Model Host로 다음 항목을 검증합니다.

- Model Host 시작과 종료
- 격리, 네트워크 차단과 자원 제한
- Evidence Pack 전달과 요청 확인
- 응답 스키마 검증, timeout과 fallback

위 실제 모델로 의미 품질과 목표 장비 지연시간을 측정한 릴리즈 근거는 아직 없습니다. 따라서 `Qwen3-4B-Instruct-2507`은 “설계상 초기 기본 모델”로만 표시하며 “CodeFlow 호환성 검증 완료 모델” 또는 “릴리즈 확정 모델”로 표시하면 안 됩니다.

실제 모델 팩을 채택할 때는 다음 값을 고정해 기록합니다.

- 공식 model ID와 immutable revision
- GGUF 양자화 방식
- 모델 파일 SHA-256
- 라이선스와 재배포 조건
- 목표 장비의 지연시간과 메모리 사용량
- Evidence reference validity, 설명 정확도, 한국어 이해도와 스키마 준수율

## 3. Model Host가 충족해야 하는 계약

CodeFlow Core는 요청마다 Model Host 프로세스를 직접 시작하고 종료합니다. Model Host는 표준 입력과 표준 출력으로 한 줄당 하나의 JSON 메시지를 처리해야 합니다.

필수 동작은 다음과 같습니다.

1. `initialize` 요청에 측정된 모델 정보와 기능을 응답합니다.
2. `semantic_enrich` 요청을 받으면 `request_received`로 요청 ID와 Evidence Pack digest를 확인합니다.
3. 제한된 Evidence Pack만 사용해 스키마에 맞는 의미 후보를 반환합니다.
4. 취소 또는 제한 시간 종료 시 즉시 종료할 수 있어야 합니다.

요청 및 응답의 기준 구현은 다음 파일에 있습니다.

- `internal/protocol/model_host.go`
- `schemas/rflsc.model-host-request.v2.schema.json`
- `schemas/rflsc.model-host-response.v2.schema.json`

## 4. CodeFlow에 Model Host 연결하기

현재는 CLI 설정 파일이 없으므로 `cmd/codeflow/main.go`에서 Model Host factory를 생성해 FlowView와 MCP 서버 설정에 전달해야 합니다.

다음 형태로 factory를 만듭니다.

```go
modelHostFactory := protocol.NewModelHostFactory(protocol.ModelHostConfig{
	BinPath:          modelHostBin,
	Args:             []string{"--model", modelFile},
	AllowedReadPaths: []string{modelFile},
	DefaultTimeout:   600 * time.Millisecond,
	ResourceLimits:   protocol.DefaultModelHostResourceLimits(),
})
```

여기서:

- `modelHostBin`은 호환 Model Host 실행 파일의 절대 경로입니다.
- `modelFile`은 GGUF 모델 파일의 절대 경로입니다.
- `AllowedReadPaths`에는 Model Host가 읽어야 하는 모델과 런타임 파일만 넣습니다.
- 저장소 경로, Git 자격 증명 경로 또는 사용자 자격 증명 디렉터리를 넣으면 Core가 실행을 거부합니다.

FlowView 설정에 factory를 전달합니다.

```go
flowview.Config{
	RepoRoot:         absTarget,
	ModelHostFactory: modelHostFactory,
}
```

MCP 설정에도 같은 방식으로 전달합니다.

```go
mcp.Config{
	RepoRoot:         absTarget,
	ModelHostFactory: modelHostFactory,
}
```

수정 후 바이너리를 다시 빌드합니다.

```sh
CGO_ENABLED=1 make build
```

## 5. 핵심 흐름 준비하기

Semantic Enrichment는 이미 발행된 최신 핵심 흐름을 대상으로 실행됩니다.

```sh
./bin/codeflow init <대상-프로젝트>
./bin/codeflow publish <대상-프로젝트>
```

발행된 Semantic Map과 현재 snapshot이 없으면 보강 요청은 `unavailable`을 반환합니다.

## 6. Semantic Enrichment 사용하기

### AI 에이전트에서 사용

CodeFlow MCP를 사용하는 에이전트가 다음 도구를 호출합니다.

```text
request_semantic_enrichment
```

최소 요청 예시는 다음과 같습니다.

```json
{
  "target": "<대상-프로젝트-절대-경로>",
  "targetStepId": "<보강할-step-id>"
}
```

특정 발행 결과에 고정하려면 `generationId`와 `computedBasisId`를 함께 전달합니다.

### FlowView API에서 사용

Model Host factory가 연결된 FlowView를 시작합니다.

```sh
./bin/codeflow view <대상-프로젝트>
```

FlowView가 출력한 URL의 `token` 값을 사용해 요청합니다.

```sh
FLOWVIEW_URL='http://127.0.0.1:4567'
FLOWVIEW_TOKEN='<FlowView가-출력한-token>'

curl -fsS -X POST \
  "$FLOWVIEW_URL/api/semantic/enrich?token=$FLOWVIEW_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"targetStepId":"<보강할-step-id>"}'
```

현재 FlowView 화면은 이 API를 자동 호출하지 않습니다. 응답 결과는 API 또는 MCP 호출 결과에서 확인해야 합니다.

## 7. 결과 확인

정상 동작하면 응답의 상태가 다음과 같습니다.

```json
{
  "state": {
    "status": "available"
  }
}
```

다음 상태는 Model Host가 사용되지 않았음을 뜻합니다.

```json
{
  "state": {
    "status": "unavailable",
    "reason": "no configured model host"
  }
}
```

`unavailable`, `timed_out` 또는 모델 응답 검증 실패가 발생해도 CodeFlow는 검증된 기본 분석 결과를 유지합니다.

## 8. 일반 사용자가 기다려야 하는 기능

다음 기능은 v0.4.0에 아직 없습니다.

- 공식 Model Host 및 검증된 모델 팩 배포
- `codeflow model install` 형태의 설치 명령
- 모델 설정 저장과 상태 확인 명령
- FlowView에서 모델 설치, 활성화 및 보강 요청을 수행하는 화면

이 기능들이 구현되기 전에는 일반 설치 사용자가 SLM을 안전하게 설정할 수 없습니다. 호환 Model Host가 없는 상태에서 원본 `llama-server`를 임의로 연결하지 마세요.
