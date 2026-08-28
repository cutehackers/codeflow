# CodeFlow LLM usage — v3 (Core Flow · 핵심 흐름)

이 문서는 LLM/코딩 에이전트가 CodeFlow v3 결과를 읽고 **핵심 흐름(아키텍처 레이어 관통)** 으로 설명하기 위한 계약. CodeFlow는 코드 근거를 만들고 LLM은 사용자 언어로 설명한다 — `unknown`을 추론으로 메우지 않는다.

## 0. 설치 — one-shot (이것만 실행)

> 한 명령으로 바이너리, Dart 어댑터 설정, Codex MCP, CodeFlow 스킬까지 설치한다. 셸 rc와 분석 대상 저장소는 수정하지 않는다.

```sh
bash scripts/install.sh
```

`scripts/install.sh`가 수행하는 일:
1. `bin/codeflow`를 `$HOME/.local/bin/codeflow`에 설치한다.
2. Codex에 `codeflow mcp`를 등록하고, 등록 시 Dart 어댑터 경로를 함께 전달한다.
3. `$HOME/.codex/skills/codeflow`에 스킬을 설치한다. 새 Codex task에서 “이메일 회원가입 핵심 흐름을 FlowView로 만들어줘”라고 바로 요청할 수 있다.

Codex MCP 이름이 이미 다른 명령에 사용 중이거나 기존 CodeFlow 스킬을 사용자가 바꿨다면 설치를 중단한다. 각각 `CODEFLOW_MCP_NAME` 또는 별도 스킬 정리 후 다시 실행한다. 사용자 설정을 덮어쓰지 않기 위한 보호다.

설치 직후 확인 (LLM이 그대로 실행):

```sh
$HOME/.local/bin/codeflow version
$HOME/.local/bin/codeflow doctor .
```

Dart SDK 3.x, Go, Codex CLI가 필요하다. 하나라도 없으면 설치가 실패하며, 불완전한 MCP 등록을 남기지 않는다.

### 삭제

설치 상태 파일이 CodeFlow가 만든 MCP·스킬·바이너리만 추적하므로 다음 한 명령으로 되돌릴 수 있다.

```sh
$HOME/.local/bin/codeflow uninstall
```

수정된 스킬, 다른 명령을 가리키는 동명 MCP, 설치기가 소유하지 않은 소스 체크아웃은 삭제하지 않고 남긴 이유를 출력한다.

## 0.1 프로젝트 아키텍처 분석 및 `codeflow.layers.yaml` 작성 가이드

CodeFlow는 핵심 흐름(Core Flow)을 검증할 때 대상 프로젝트 루트의 `codeflow.layers.yaml`을 기준으로 레이어 정규화, 단조 증가 순서 검증(monotonic layer progression), 파일 경로 패턴(`pathPatterns`) 매칭을 수행한다.

> **설치/초기화 시 LLM 핵심 지침**: 대상 프로젝트가 특정 언어나 프레임워크에 국한되지 않고 어떤 아키텍처 패턴(Feature-first, Layer-first, Clean, Hexagonal/Ports & Adapters, FSD, CQRS, MVC/MVVM, Monorepo 등)을 채택하고 있더라도, 에이전트는 **프로젝트의 구조와 역할을 범용적으로 분석하여 최적화된 `codeflow.layers.yaml`을 생성**해야 한다.

### 1) YAML 스키마 및 규격 (`schemas/layers-config.schema.json`)

```yaml
version: 1 # (필수) 항상 1
strictOrder: true # (기본값: true) 레이어 역행 시 에러 처리. false면 warning + unknowns[] 기록
allowUnknownLayer: false # (기본값: false) 미정의 레이어는 에러 처리. true면 unknown으로 매핑 후 경고
layers: # (필수) 프로젝트 아키텍처에 존재하는 레이어 목록 (진입점 -> 말단 순서로 배치)
  - name: presentation # (필수) 7대 표준 Canonical 레이어 명칭 중 하나
    aliases: [ui, view, widget, screen, page] # 프로젝트에서 쓰이는 관용적 별칭/개념명 (소문자 정규화)
    pathPatterns: ["**/presentation/**", "**/ui/**"] # glob 패턴 목록 (anchor.repoRelativePath 검증)
```

### 2) 7대 표준 Canonical 레이어 범용 매핑 매트릭스

CodeFlow는 아키텍처 스타일과 언어에 관계없이 모든 계층을 7대 Canonical 레이어로 정규화하여 검증 및 시각화(FlowView Lane)한다. `name`은 반드시 아래 7개 중 하나를 선택한다.

| Canonical `name` | 본질적 책임 (Core Responsibility) | 프론트엔드/모바일 (Flutter, React, iOS/Android 등) | 백엔드/마이크로서비스 (Go, NestJS, Spring, FastAPI 등) | 관용적 별칭 (`aliases`) |
|---|---|---|---|---|
| `presentation` | 사용자 인터페이스 렌더링, 뷰 이벤트 수신, 화면 전환 | View, Screen, Page, Widget, Component, Dialog | Template (SSR), CLI View, Swagger/OpenAPI | `ui`, `view`, `widget`, `screen`, `page`, `component` |
| `controller` | UI 이벤트/HTTP 요청 수신, 상태 관리, 뷰모델, 입력 바인딩 | ViewModel, Notifier, BLoC, Cubit, Store, Hook, Reducer | HTTP Controller, Router, GraphQL Resolver, gRPC Handler | `controller`, `notifier`, `bloc`, `cubit`, `viewmodel`, `store`, `handler`, `resolver` |
| `usecase` | 애플리케이션 비즈니스 유스케이스 조율, 트랜잭션/워크플로우 실행 | UseCase, Interactor, AppService, Workflow | Application Service, UseCase, Command/Query Handler, Orchestrator | `usecase`, `use_case`, `service`, `application`, `interactor`, `command`, `query` |
| `domain` | 핵심 엔티티, 도메인 비즈니스 규칙, 값 객체(VO), 순수 계산 | Entity, Model, ValueObject, Policy, Rule | Domain Entity, Aggregate Root, Domain Service, Domain Event | `domain`, `entity`, `model`, `aggregate`, `vo` |
| `data` | 데이터 영속성 처리, DB/캐시 연동, DTO 변환, 리포지토리 구현 | Repository, DataSource, LocalCache, DAO, DTO | Repository Impl, DAO, ORM Mapper, DataSource, Cache | `data`, `repository`, `datasource`, `data_source`, `dao`, `mapper` |
| `infra` | OS/플랫폼 연동, 하드웨어 장치 제어, 시스템 보안/스토리지 | SecureStorage, Device/Sensors, Platform Channel | Config, Message Queue, Log/Metrics, Database Driver, OS | `infra`, `infrastructure`, `platform`, `storage`, `system` |
| `external` | 외부 타사 서비스 연동, 원격 REST/gRPC 통신, 3rd party SDK | HTTP Client, Remote API, Payment SDK, OAuth | Remote REST Client, 3rd-party Gateway, Webhook Sender | `external`, `api`, `remote`, `client`, `gateway`, `network` |

### 3) 범용 아키텍처 분석 4단계 프로세스 (LLM 에이전트 행동 지침)

에이전트는 대상 저장소 분석 시 다음 4단계를 체계적으로 밟는다:

#### Step 1: 프로젝트 프로파일링 (Manifest & Framework 파악)
- 루트 매니페스트 파일(`package.json`, `pubspec.yaml`, `go.mod`, `pom.xml`, `build.gradle`, `Cargo.toml`, `pyproject.toml` 등)을 확인하여 언어, 런타임, 주요 프레임워크/라이브러리를 파악한다.
- 모노레포/멀티패키지 구조(`apps/*`, `packages/*`, `modules/*`)인지 단일 프로젝트인지 확인한다.

#### Step 2: 디렉터리 조직 전략 (Organization Strategy) 판별
- **Feature-Driven / Vertical Slice**: 기능별로 디렉터리가 나뉘고 내부에 계층이 포함된 구조 (`features/<name>/presentation/...`, `modules/<name>/domain/...`)
- **Layer-Driven / Horizontal**: 최상위에 계층이 나뉘어 있는 구조 (`controllers/`, `services/`, `repositories/`, `views/` 등)
- **Feature-Sliced Design (FSD)**: 슬라이스 계층 구조 (`app/`, `pages/`, `widgets/`, `features/`, `entities/`, `shared/`)
- **Hexagonal / Ports & Adapters / Onion**: 내부 도메인과 외부 어댑터가 분리된 구조 (`core/`, `domain/`, `application/`, `adapters/inbound`, `adapters/outbound`, `ports/`)

#### Step 3: 레이어 선별 및 단조 증가 순서 결정 (Layer Pruning & Ordering)
- **필요한 레이어만 선별**: 프로젝트에 존재하지 않는 레이어는 과감히 생략한다. (예: Usecase 없이 Controller에서 바로 Repository를 호출하는 단순 구조면 `presentation → controller → data`만 선언)
- **단조 증가 순서 배치**: 이벤트/요청 진입(최상단)부터 데이터/외부(최하단) 방향으로 순서를 구성한다.

#### Step 4: `pathPatterns` 및 `aliases` 추출 및 `codeflow.layers.yaml` 작성
- 프로젝트의 실제 폴더 구조를 커버하는 doublestar glob 패턴(`**`)을 작성한다.
- 팀/프로젝트 고유의 명칭(예: `bloc`, `resolver`, `command`, `saga`, `dao` 등)을 `aliases`에 등록한다.
- 대상 프로젝트 루트 경로(`codeflow.layers.yaml`)에 저장한다.

### 4) 5대 주요 아키텍처 패턴별 작성 템플릿

#### 템플릿 1: Feature-First Clean Architecture (Flutter, React, 모바일/웹 공통)
```yaml
version: 1
strictOrder: true
allowUnknownLayer: false
layers:
  - name: presentation
    aliases: [ui, view, widget, screen, page, component, dialog]
    pathPatterns:
      - "**/features/**/presentation/views/**"
      - "**/features/**/presentation/widgets/**"
      - "**/features/**/presentation/components/**"
      - "**/core/widgets/**"
  - name: controller
    aliases: [notifier, provider, controller, bloc, cubit, viewmodel, store]
    pathPatterns:
      - "**/features/**/presentation/controllers/**"
      - "**/features/**/presentation/notifiers/**"
      - "**/features/**/presentation/blocs/**"
      - "**/features/**/presentation/viewmodels/**"
  - name: usecase
    aliases: [usecase, use_case, application, interactor, workflow]
    pathPatterns:
      - "**/features/**/domain/usecases/**"
      - "**/features/**/domain/interactors/**"
  - name: domain
    aliases: [domain, entity, model, aggregate, value_object]
    pathPatterns:
      - "**/features/**/domain/entities/**"
      - "**/features/**/domain/models/**"
  - name: data
    aliases: [repository, datasource, data_source, local_source, dao]
    pathPatterns:
      - "**/features/**/data/repositories/**"
      - "**/features/**/data/datasources/**"
  - name: infra
    aliases: [infra, infrastructure, platform, storage, device]
    pathPatterns:
      - "**/core/platform/**"
      - "**/core/storage/**"
      - "**/infrastructure/**"
  - name: external
    aliases: [api, remote, client, gateway, network]
    pathPatterns:
      - "**/core/network/**"
      - "**/features/**/data/datasources/remote/**"
      - "**/external/**"
```

#### 템플릿 2: Hexagonal / Ports & Adapters Architecture (Go, Java/Spring, NestJS, FastAPI 등 백엔드)
```yaml
version: 1
strictOrder: true
allowUnknownLayer: false
layers:
  - name: controller
    aliases: [adapter_in, inbound_adapter, http, rest, grpc_handler, resolver, router]
    pathPatterns:
      - "**/adapters/inbound/**"
      - "**/internal/delivery/http/**"
      - "**/internal/delivery/grpc/**"
      - "**/interfaces/controllers/**"
  - name: usecase
    aliases: [application, app_service, command_handler, query_handler, usecase]
    pathPatterns:
      - "**/application/usecases/**"
      - "**/application/services/**"
      - "**/application/commands/**"
      - "**/application/queries/**"
  - name: domain
    aliases: [domain, entity, aggregate, domain_service, event]
    pathPatterns:
      - "**/domain/entities/**"
      - "**/domain/models/**"
      - "**/domain/services/**"
  - name: data
    aliases: [adapter_out, outbound_adapter, repository_impl, persistence, dao]
    pathPatterns:
      - "**/adapters/outbound/persistence/**"
      - "**/infrastructure/persistence/**"
      - "**/internal/repository/**"
  - name: infra
    aliases: [infrastructure, config, database, messaging, telemetry]
    pathPatterns:
      - "**/infrastructure/config/**"
      - "**/infrastructure/database/**"
      - "**/pkg/**"
  - name: external
    aliases: [external_client, remote_adapter, 3rd_party, gateway]
    pathPatterns:
      - "**/adapters/outbound/clients/**"
      - "**/infrastructure/external/**"
```

#### 템플릿 3: Feature-Sliced Design (FSD - 모던 프론트엔드 / Next.js, React, Vue)
```yaml
version: 1
strictOrder: true
allowUnknownLayer: false
layers:
  - name: presentation
    aliases: [app, pages, widgets, views, ui]
    pathPatterns:
      - "src/app/**"
      - "src/pages/**"
      - "src/widgets/**"
      - "src/features/**/ui/**"
      - "src/entities/**/ui/**"
      - "src/shared/ui/**"
  - name: controller
    aliases: [feature_model, entity_model, hook, store]
    pathPatterns:
      - "src/features/**/model/**"
      - "src/entities/**/model/**"
      - "src/shared/lib/hooks/**"
  - name: usecase
    aliases: [lib, actions, logic, workflow]
    pathPatterns:
      - "src/features/**/lib/**"
      - "src/entities/**/lib/**"
  - name: domain
    aliases: [types, contracts, schemas]
    pathPatterns:
      - "src/entities/**/types/**"
      - "src/shared/types/**"
  - name: external
    aliases: [api, client, queries, mutations]
    pathPatterns:
      - "src/features/**/api/**"
      - "src/entities/**/api/**"
      - "src/shared/api/**"
```

#### 템플릿 4: 경량 Layered MVC / MVVM 아키텍처
```yaml
version: 1
strictOrder: true
allowUnknownLayer: false
layers:
  - name: presentation
    aliases: [view, ui, screen, pages, template]
    pathPatterns: ["views/**", "screens/**", "ui/**", "pages/**"]
  - name: controller
    aliases: [controller, viewmodel, handler]
    pathPatterns: ["controllers/**", "viewmodels/**", "handlers/**"]
  - name: usecase
    aliases: [service, manager, logic]
    pathPatterns: ["services/**", "managers/**", "logic/**"]
  - name: domain
    aliases: [model, entity, schema]
    pathPatterns: ["models/**", "entities/**", "schemas/**"]
  - name: data
    aliases: [repository, datasource, db, dao]
    pathPatterns: ["repositories/**", "datasources/**", "db/**", "dao/**"]
  - name: external
    aliases: [client, api, remote, network]
    pathPatterns: ["api/**", "clients/**", "network/**"]
```

#### 템플릿 5: Monorepo / Multi-Package 아키텍처
```yaml
version: 1
strictOrder: true
allowUnknownLayer: false
layers:
  - name: presentation
    aliases: [ui_package, app_ui, widgets]
    pathPatterns:
      - "apps/**/presentation/**"
      - "packages/ui/**"
      - "packages/feature_*/presentation/**"
  - name: controller
    aliases: [state, notifiers, controllers]
    pathPatterns:
      - "apps/**/controllers/**"
      - "packages/feature_*/controllers/**"
      - "packages/state/**"
  - name: usecase
    aliases: [domain_services, usecases]
    pathPatterns:
      - "packages/domain/**/usecases/**"
      - "packages/feature_*/domain/usecases/**"
  - name: domain
    aliases: [core_domain, entities, models]
    pathPatterns:
      - "packages/domain/**/entities/**"
      - "packages/models/**"
  - name: data
    aliases: [data_package, repositories]
    pathPatterns:
      - "packages/data/**"
      - "packages/repositories/**"
  - name: infra
    aliases: [core_package, platform]
    pathPatterns:
      - "packages/core/**"
      - "packages/platform/**"
  - name: external
    aliases: [api_client, network_package]
    pathPatterns:
      - "packages/network/**"
      - "packages/api_client/**"
```

## 1. 가장 짧은 사용 흐름

사용자 프롬프트 예: "이메일 회원가입 핵심 흐름을 FlowView로 만들어줘" / "회원가입 핵심 흐름 보여줘" / "core flow for email signup"

에이전트는 설치된 CodeFlow 스킬(`skills/codeflow/SKILL.md` v3)을 따른다:

```
explore (진입 레이어 이벤트 + 레이어 관통 추적) → publish_core_flow (중간 산출물 검증 게시) → get_flow_payload + unknowns 확인 → 요청한 경우 open_review
```

alternative (탐색·브라우징, 핵심 흐름 아님):

```
harvest_flows (후보 탐색) → get_flow_payload → open_review
```

`harvest_flows`는 `discovery (browsing, not core)`로 레이블되며 핵심 흐름 발행에는 쓰지 않는다. 핵심 흐름은 반드시 `publish_core_flow`로 발행한다.

```sh
# MCP가 없는 환경의 CLI 대체 (설치 후에는 env 없이도 동작) — discovery 트랙
codeflow flows --json ./testdata/example_app
codeflow publish ./testdata/example_app
codeflow show flow-7232d63b96bd6efa --json | python3 -m json.tool
```

MCP가 있으면 기존 Core 재사용 — 매번 `init/serve` 불필요. 핵심 흐름은 MCP 전용 `publish_core_flow`로 게시한다 (CLI 없음).

## 2. MCP 도구 선택 (v3 8종)

항상 정확한 `flowId` 또는 `entrySymbolPath`를 전달 — 기본값에 의존 금지.

| 목적 | 도구 | 입력 | 사용 시점 |
|---|---|---|---|
| 핵심 흐름 발행 | `publish_core_flow` | `artifact{entrySymbolPath,title,description?,layers?,steps[anchor+layer],edges?,unknowns?}`, `token?` | 사용자 요청 핵심 흐름 — 앵커/레이어 검증 후 원자적 게시 (`codeflow.layers.yaml` 있으면 그에 맞춰 검증) |
| 후보 탐색 | `harvest_flows` | `target?`, `query?` (예: "이메일 회원가입") | 브라우징·탐색 — `intentSignals{derivedName, docLine, className}`으로 NL 매칭. 핵심 흐름 발행에는 사용 금지 |
| 단일 흐름 읽기 | `get_flow_payload` | `flowId` 또는 `entrySymbolPath` | 후보 1개 상세 조회 |
| 임의 진입점 분석 | `analyze_flow` | `entrySymbolPath` (예: `lib/features/auth/email_signup_notifier.dart#EmailSignupNotifier.submit`) | `harvest`에 없는 진입점 — 즉시 slice+fuse+게시 (기존 generation에 병합) |
| 세션 근거 제출 | `submit_flow_draft` | `artifact` (anchor 필수), `token?` | 에이전트가 여정 근거를 보강할 때 — `repoRelativePath+byteRange+fileHash+spanHash+enclosingSymbolPath+canonicalAstFingerprint` 전체 필요 |
| 단계 승인 | `approve_step` | `flowId`, `symbolPath`, `name`, `rules?`, `token?` | 사용자가 이름·규칙 승인 시 (E3, provenance=approved) |
| 미확정 확인 | `report_unknowns` | `flowId?` (없으면 전체) | 설명이 추론에 의존하는 즉시 |
| FlowView 열기 | `open_review` | `flowId?` | 사용자가 화면 확인 요청 시만 — MCP가 FlowView를 지연 기동하고 `?token=&flow=` 포함 URL 반환 |

`query` 예: `harvest_flows {"query":"이메일 회원가입"}` → `candidates[].intentSignals.derivedName="이메일을 회원가입한다"`와 부분일치.

`analyze_flow`는 파괴적이지 않음 — 기존 generation을 읽어 병합 후 게시.

`publish_core_flow` 입력 `artifact`는 `schemas/core-artifact.schema.json`을 따른다. `steps[].layer`는 `presentation, controller, usecase, domain, data, infra, external` 중 하나 (canonical English, CORE가 `codeflow.layers.yaml` aliases로 정규화). `layers[]`가 있으면 스텝 순서가 그 트래버설에 단조 증가해야 한다 (branch 제외). 초과 시 `artifact_too_large` → 분할 발행.

`codeflow.layers.yaml`가 있으면 모든 layer/alias/pathPatterns는 그 파일을 따른다. 에이전트는 탐색 전 이 파일을 먼저 읽는다. 없으면 CORE는 8 canonical 레이어와 내장 alias 테이블로 검증한다.

Discovery publish는 비즈니스 흐름만 발행한다 — UseCase 단독 진입(`use_case_invocation`) 흐름은 제외되며, 유스케이스 내부는 상위 비즈니스 흐름의 call 단계와 `edges[]` 위임으로 드러난다. UseCase 단독 분석이 필요하면 `analyze_flow`로 해당 진입점을 직접 요청한다.

## 3. 응답을 읽는 순서

FlowSpec envelope:
```
flowId → title → description → basisSha(64hex) → generatedAt → steps[] → edges[] → truncated? → unknowns[] + view URL
```
`steps[]` 각 원소:
```
ordinal → name → layer(presentation|controller|usecase|domain|data|infra|external|unknown) → kind(mutation|call|guard|branch) → provenance(approved|session|derived|unknown) → freshness(fresh|stale|orphaned) → confidence → basisSha → anchor → rules/stateDelta/sideEffect/branch/codeLens
```
`edges[]` 각 원소 (레이어 간 위임):
```
stepOrdinal → toSymbolPath → kind(resolved_cross_file|boundary_call|unknown_edge) → resolutionStatus(resolved|unresolved_dynamic|unresolved_type|truncated) → toLayer?
```

LLM 읽기 순서:
1. `basisSha`·`generatedAt`으로 어떤 스냅샷인지 확인.
2. `description`(진입점 docLine 융합)으로 비즈니스 목적 첫 문장을 만든다.
3. `steps`를 `layer`의 canonical 순서 `presentation→controller→usecase→domain→data→infra→external→unknown` (`codeflow.layers.yaml`가 있으면 그 순서) 로, 같은 layer 내에서는 `ordinal` 순으로 — `provenance` 권위 `approved>session>derived>unknown`, `freshness`가 `stale/orphaned`면 승인 큐로 안내. `kind`로 핵심(mutation/call)과 보조(guard/branch)를 구분해 설명 밀도를 조절. `resolutionStatus=unresolved_dynamic`이면 추론하지 않는다.
4. `stateDelta {before→after}`, `sideEffect`, `branch`, `edges[]`로 인과와 위임 연결 — `resolutionStatus=unresolved_dynamic`인 엣지는 "동적 호출로 끊긴 곳"이라고 정직하게 말한다. `edges[].toLayer`로 레인 홉을 설명.
5. `truncated=true`면 잘린 하위 흐름이 있음을 함께 안내한다.
6. `codeLens {path,startLine,endLine}`로 코드 근거 제시 — 줄 번호를 만들지 말고 anchor 그대로 사용.
7. `unknowns[]`는 반드시 언급 — 비어 있어도 "확인되지 않은 부분 없음"으로 명시.
8. `open_review`의 `url`은 `?token=` 포함 — 그대로 제공. FlowView는 레이어별 Architecture Map(핵심 흐름은 명시적 `layer`로 레인 렌더, 레거시는 InferLayer 폴백)과 핵심 타임라인(전체 보기 토글 포함)을 보여준다.

`codeflow.layers.yaml`가 있으면 모든 layer/alias/pathPatterns는 그 파일을 따른다. 에이전트는 탐색 전 이 파일을 먼저 읽는다.

## 4. 신뢰 상태 규칙

| 상태 | 말할 수 있는 것 | 금지 |
|---|---|---|
| `fresh + derived/session` | 코드 근거로 확인됐다고 설명 | 런타임에 반드시 실행됐다고 단정 |
| `stale` | 코드가 바뀌어 재승인 필요, `refresh`/`publish` 안내 | 이전 anchor를 현재 근거로 사용 |
| `orphaned` | 심볼이 사라짐, 승인 철회 필요 | 다른 심볼로 대체 |
| `unknown` | 어디까지 확인됐고 무엇이 빠졌는지 설명 | 그럴듯한 대상·상태를 선택 |

## 5. 사용자에게 설명하는 형식

```
비즈니스 여정 요약
이메일을 이용한 회원가입 — 사용자가 이메일을 제출해 가입을 완료하는 경로

현재 코드 흐름 (EmailSignupNotifier.submit) — presentation→controller→usecase→data
1. [presentation] 입력 규칙을 검증한다 [derived, fresh] — lib/features/auth/email_signup_notifier.dart:37
2. [controller] 진행 상태로 갱신한다 — :38
3. [usecase] 외부 서비스에 작업을 요청한다: SignupService.call [derived, fresh] — :39
4. [data] 성공 상태로 갱신한다 — :40

상태 변화
idle → submitting → done / failed(error: 'signup failed')

레이어 위임
presentation → controller → usecase(SignupService.call, 3단계에서 위임) → data

확인되지 않은 부분
없음 (stale 0, unknowns 0)

FlowView에서 코드 렌즈(심볼 단위 함수 본문 + 단계 포커스 강조)와 함께 시각 검증 가능 — URL: http://127.0.0.1:4567/?token=...&flow=flow-7232d63b96bd6efa
```

원칙: **비즈니스 목적을 첫 문장에**, 원인 순서로, `unknowns`가 있으면 “왜 남았는지/다음에 확인할 코드”로. 핵심 흐름은 `layer` 순서로 설명한다.

## 6. 변경 후 재확인

코드 변경 시 이전 결과 재사용 금지 — `watch`가 500ms 폴링+mtime 필터로 감지하나 즉시 필요하면:
```sh
codeflow publish <repo>
codeflow show <flowId> --json
```
`basisSha`가 바뀌었는지 확인 후 다시 설명.

## 7. 금지 사항

- CodeFlow와 별개 스캐너를 즉석 구현하지 않는다.
- product source를 CodeFlow 사용을 위해 수정하지 않는다.
- `unknown`을 `observed`로 승격하지 않는다.
- `token`, `.codeflow/pointer.json`, 비밀 값을 노출하지 않는다.
- 사용자가 요청하지 않았는데 FlowView를 열거나 외부에 게시하지 않는다.

## 8. LLM 최소 체크리스트

```
[ ] 핵심 흐름 요청이면 publish_core_flow로 발행했는가? (harvest_flows는 탐색 전용)
[ ] publish_core_flow artifact의 모든 앵커가 검증됐는가? layer가 codeflow.layers.yaml에 정의된 값인가? layer 순서가 단조 증가하는가 (branch 제외)?
[ ] query로 harvest_flows 후 candidateId/flowId를 정확히 사용했는가? (탐색 트랙)
[ ] freshness가 stale/orphaned면 승인 큐로 안내했는가?
[ ] steps를 layer 순서(presentation→…→external) + ordinal 순, provenance 권위 순으로 읽었는가?
[ ] edges[]의 위임(toLayer)과 unresolved_dynamic 끊김을 추측 없이 설명했는가?
[ ] unknowns를 추측 없이 설명했는가?
[ ] 코드는 anchor/codeLens 그대로 인용했는가?
[ ] FlowView는 요청 시에만 open_review URL(token 포함)로 제공했는가?
```

로컬 실행·캐시 관리는 `docs/local-usage.md`, 전체 설계는 `docs/design-v2.md` 참조.
