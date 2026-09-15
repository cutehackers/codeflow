# FlowView Macro Context Storyboard 비즈니스 관문 시퀀스 스펙

- Contract ID: `FLOWVIEW-MACRO-CONTEXT-STORYBOARD`
- Contract Status: Approved
- Created: 2026-09-14
- Intent Status: Hardened
- Source: 사용자 요청 ("우리가 과거에 architecture map으로 보여줬던걸 ' Macro Context Storyboard' 으로 보여주려하는데 너느 ㄴ아직까지 아키텍쳐 모듈들을 그대로 보여주고 있는데 이건 아니올시다. 올바른 흐름의 예는 Screenshot 2026-09-14 at 6.30.35 PM.png 이런 이미지라고 볼 수 있겠다. /codify 스팩으로 먼저 구현내용을 고정하라.")
- Decision Records: [`docs/design/decisions/2026-09-14-flowview-macro-context-storyboard-ko.md`](../decisions/2026-09-14-flowview-macro-context-storyboard-ko.md)
- Glossary: [`docs/design/glossary.md`](../glossary.md)

---

## 1. 문제와 목표 (Problem and Goal)

기존 FlowView의 `Architecture Map`은 소스 코드의 패키지/폴더/모듈(Presentation, UseCase, Domain, Data 등)을 정적으로 나열하는 컴포넌트 구조도에 불과했다.
이 방식은 단일 실행 흐름에서 **"실제 어떤 비즈니스 사건이 발생하고, 어떤 검증과 도메인 규칙을 거쳐 처리되는가"**라는 개발자의 핵심 인지 질문에 답하지 못한다.

최신 개편 화면에서도 단순 기술 계층(`layer: presentation`)과 프로그래밍 식별자(`CheckoutPage.submit`, `CheckoutService.execute`)를 그대로 프레임으로 렌더링하여 여전히 "기술 모듈 나열" 수준에 머물러 있었다.

본 스펙의 목표는 확정 디자인 스크린샷(`Screenshot 2026-09-14 at 6.30.35 PM.png`) 및 `live-semantic-map-final.html`의 검증된 문법을 기준으로, **Macro Context Storyboard를 기술 모듈 나열이 아닌 'End-to-End 비즈니스 관문(Gateway) 시퀀스'로 완전 승격**하고 구현 규격을 확정하는 것이다.

### Intent 및 Goal 정의
* **INT-STORYBOARD-01**: 정적 아키텍처 모듈도를 탈피하고, 비즈니스 관문과 사건 중심의 Macro Context Storyboard를 구축한다.
* **GOAL-STORYBOARD-01**: End-to-End 5개 핵심 관문 역할(`UI EVENT`, `GATEWAY`, `DOMAIN CORE`, `APPLICATION`, `EXTERNAL PG`) 및 전용 뷰모델(`StoryFrame`) 계약을 확립한다.
* **GOAL-STORYBOARD-02**: 디자인 스크린샷과 100% 일치하는 네오-브루탈리즘 레이아웃(2px 볼드 보더, 5열 균등 그리드, `ACTIVE SURGERY` 배지, 번개 아이콘, 점선 구분선)을 구현한다.
* **GOAL-STORYBOARD-03**: 선택/수정(Active Surgery) 상태와 중앙 코드 뷰 및 우측 레이더 간의 실시간 양방향 상호작용 동기화를 보장한다.
* **GOAL-STORYBOARD-04**: Anti-Telemetry Guard를 준수하여 내부 컴파일러 텔레메트리 누출 없이 오직 검증 가능한 소스 코드와 비즈니스 내러티브만 노출한다.

---

## 2. 범위 (Scope)

### 포함 범위 (In Scope)
1. **스토리보드 전용 뷰모델 (`StoryFrame`) 계약**:
   - `frameOrdinal`: `FRAME 01` ~ `FRAME 05`
   - `gatewayRole`: `UI EVENT`, `GATEWAY`, `DOMAIN CORE`, `APPLICATION`, `EXTERNAL PG` 등 맥락형 관문 역할
   - `businessTitle`: 비즈니스 목적 및 사건명 (예: `고객 결제 버튼 클릭`, `실시간 재고 검증 & 선점`)
   - `businessNarrative`: 1~2줄 비즈니스 행동 설명 (예: `품목 잔여량 확인, 10분간 선점 락 부여 및 토큰 발행`)
   - `technicalAnchor`: 실행 기술 심볼 (예: `InventoryService.checkStock()`)
   - `surgeryBadge`: `ACTIVE SURGERY` 배지
   - `deltaTag`: `+ NEW SURGERY`, `~ RULE CHG`, `- REMOVED`
   - `isHighlighted`: `⚡` 번개 마크 표시
2. **시각적 레이아웃 및 스타일 규격 (`MacroStoryboard.svelte`)**:
   - 상단 섹션 보더: `border: 2px solid var(--ink); border-radius: 9px; box-shadow: 3px 3px 0 var(--soft);`
   - 헤더 텍스트: `2. MACRO CONTEXT STORYBOARD · 전체 비즈니스 흐름 조망 (5개 관문 엔드투엔드 시퀀스)`
   - 우측 상태 캡슐: `+1 Step Added · 1 Rule Modified` (모노스페이스 볼드 폰트 알약 배지)
   - 관문 트랙 그리드: `display: grid; grid-template-columns: repeat(5, 1fr); gap: 10px;`
   - 카드 기본/선택/수술 스타일:
     - 기본: `border: 1px solid var(--line); border-radius: 7px; background: #fafaf8; padding: 10px 12px;`
     - 활성/선택: `border: 2px solid var(--ink); background: var(--paper, #fff); box-shadow: 2px 2px 0 var(--ink);`
     - 수술(Active Surgery): 상단 우측 `ACTIVE SURGERY` 블랙 필 배지 + `+ NEW SURGERY` 태그 + `⚡` 아이콘
     - 구분선: 설명과 기술 심볼 사이 점선(`border-top: 1px dashed var(--line);`)
3. **샘플 및 실시간 데이터 승격 (`sampleData.ts`, `flowStore.svelte.ts`)**:
   - 스크린샷과 동일한 이커머스 체크아웃 5대 관문 시퀀스 데이터 구성
   - 백엔드 `semanticMap` 단계로부터 비즈니스 관문 역할을 자동 승격하는 헬퍼 구축

### 제외 범위 (Non-Goals)
* 내부 컴파일러 텔레메트리(에포크 번호, 정착 ms, 컴파일러 캐시 메트릭) 표시 (Anti-Telemetry 규칙)
* 백엔드 저장소의 영구 데이터베이스 스키마 파괴적 변경 (기존 `Step`, `SemanticMapIR` 상위 호환 유지)

---

## 3. 액터 및 전제조건

* **Primary Actor**: 비즈니스 흐름과 코드 변경 위치를 빠르고 정확하게 파악하려는 엔지니어
* **시스템 전제조건**:
  * Svelte 5 Runes + TypeScript + Vite Singlefile 프론트엔드 환경
  * 브라우저: CSS Grid 및 현대 표준을 지원하는 브라우저

---

## 4. 확정된 사실 (Confirmed Facts)

1. 스크린샷([`Screenshot 2026-09-14 at 6.30.35 PM.png`](file:///Users/junhyounglee/Desktop/Screenshot%202026-09-14%20at%206.30.35%20PM.png))은 `live-semantic-map-final.html`에서 검증된 공식 디자인 사양이다.
2. 스토리보드는 가로 스크롤 나열보다 **5개 관문이 전체 너비를 채우는 그리드 형태(`grid-template-columns: repeat(5, 1fr)`)**가 화면의 시각적 안정성과 비즈니스 조망 능력을 극대화한다.
3. 수술(Active Surgery) 단계는 테두리 강조뿐만 아니라, `ACTIVE SURGERY` 검정 배지와 `⚡` 번개 아이콘으로 명확하게 구별되어야 한다.

---

## 5. 가정 (Assumptions)

* **ASM-01**: 일반적인 비즈니스 트랜잭션 흐름은 4~7개(기본 5개)의 핵심 관문으로 축약·조망할 수 있다.
  - *검증 방법*: 이커머스, 인증, 주문, 승인 흐름의 실사례 검토를 통해 확인됨.
  - *거짓일 경우의 대응*: 관문이 5개를 초과할 경우 `grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));`로 자연스럽게 줄바꿈 또는 가로 반응형 확장 지원.

---

## 6. 비즈니스 규칙 및 불변식 (Invariants)

* **INV-01 (비즈니스 관문 우선)**: 스토리보드 카드의 주 제목(`story-title`)은 기술 함수명이 아닌, **사용자/비즈니스 관점의 사건/목적**이어야 한다. 기술 함수명은 하단 점선 아래 푸터(`story-footer`)에 배치한다.
* **INV-02 (단일 선택 동기화)**: 스토리보드에서 선택된 관문(`active-selected`)은 중앙 코드 뷰, 좌측 레일, 우측 레이더의 타겟 심볼과 100% 동일한 `stepId`를 공유하며 즉시 동기화되어야 한다.
* **INV-03 (수술 위치 가시성)**: `semanticDelta`에 의해 규칙 변경(`changed_rule`)이나 동작 추가(`added_behavior`)가 감지된 관문은 반드시 `ACTIVE SURGERY` 또는 `CHANGED` 배지와 `⚡` 아이콘을 부여하여 변경 위치를 1초 만에 식별할 수 있어야 한다.
* **INV-04 (Anti-Telemetry Preservation)**: 스토리보드 상단 배지 및 설명에 컴파일러 내부 상태나 텔레메트리 지표를 절대 표시하지 않는다.

---

## 7. 관측 가능한 사용자 흐름 (Observable User Flow)

1. 사용자가 FlowView 페이지에 접속하거나 흐름을 조회한다.
2. 상단에 볼드 테두리의 **`2. MACRO CONTEXT STORYBOARD`**가 나타나며, 전체 너비를 채우는 5개 관문 카드가 표시된다.
   - `FRAME 01 · UI EVENT`: 고객 결제 버튼 클릭
   - `FRAME 02 · GATEWAY`: 장바구니 정합성 검증 (`~ RULE CHG`)
   - `FRAME 03 · DOMAIN CORE`: ⚡ 실시간 재고 검증 & 선점 (`ACTIVE SURGERY`, `+ NEW SURGERY`)
   - `FRAME 04 · APPLICATION`: 주문서 생성 & 승인 대기
   - `FRAME 05 · EXTERNAL PG`: PG사 결제 승인 API
3. 사용자가 `FRAME 03` 카드를 클릭한다.
4. `FRAME 03` 카드가 2px 볼드 블랙 테두리와 `ACTIVE SURGERY` 배지로 활성화되며, 중앙 코드 패널이 `InventoryService.checkStock()` 소스 위치로 부드럽게 스크롤된다.
5. 우측 Blast Radius Radar의 중심 타겟이 `checkStock`으로 전환되며, 관련 상위 호출자와 파괴 테스트가 동심원에 즉각 렌더링된다.

---

## 8. 예외 및 경계 동작 (Failure and Boundary Behavior)

* **관문 수가 5개 미만인 경우**: 그리드 컬럼 수에 맞춰 남은 영역을 균등하게 배분하여 빈 공간으로 인한 어색함이 없도록 렌더링한다.
* **관문 수가 6개 이상인 경우**: `repeat(auto-fit, minmax(180px, 1fr))`로 카드 크기를 안정적으로 유지하며 레이아웃이 깨지지 않도록 수용한다.
* **비즈니스 설명 텍스트가 긴 경우**: 카드 높이를 유연하게 맞추되, 제목은 최대 2줄(`line-clamp: 2`), 설명은 가독성을 저해하지 않도록 정돈한다.

---

## 9. 결정 사항 표 (Decisions)

| ID | 질문 | 결정 | 근거 | 기각된 대안 | 결과 |
|---|---|---|---|---|---|
| D-01 | 스토리보드에 무엇을 표시할 것인가? | 아키텍처 모듈이 아닌 비즈니스 관문 시퀀스 | 개발자의 1차 인지 목표는 시스템 비즈니스 사건의 이해임 | 기술 계층/패키지 단순 나열 | 관문 역할(`UI EVENT`, `GATEWAY` 등) 및 비즈니스 내러티브 도입 |
| D-02 | 레이아웃 구조는 어떻게 할 것인가? | 5열 균등 그리드 (`grid-template-columns: repeat(5, 1fr)`) | 화면 전체 폭을 활용하여 엔드투엔드 흐름을 한눈에 조망 | 좁은 가로 스크롤 트랙 | 스크린샷과 100% 일치하는 시원한 뷰 제공 |
| D-03 | 수술/변경 표시는 어떻게 할 것인가? | `ACTIVE SURGERY` 블랙 필 + `+ NEW SURGERY` 태그 + `⚡` 아이콘 | 변경 위치의 직관적 식별 | 단순 텍스트 레이블 | 시각적 위계 확립 및 명확한 포커스 제공 |
