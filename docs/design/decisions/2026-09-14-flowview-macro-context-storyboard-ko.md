# FlowView Macro Context Storyboard 비즈니스 관문 시퀀스 전환 결정기록

- Decision ID: `DEC-FLOWVIEW-MACRO-CONTEXT-STORYBOARD-01`
- Status: Approved
- Date: 2026-09-14
- Scope: Macro Context Storyboard의 역할을 정적 Architecture Map(패키지/모듈 분할도)에서 End-to-End 비즈니스 관문(Gateway) 시퀀스로 승격
- Related Spec: `docs/design/specs/2026-09-14-flowview-macro-context-storyboard-ko.md`

---

## 1. 맥락과 문제 (Context & Problem)

과거 FlowView의 `Architecture Map`은 소스 코드의 패키지/모듈 구조(UI, Controller, UseCase, Domain, Repository 등)를 박스나 레인으로 나열하는 정적 구조도에 머물렀다.
이 방식은 현재 분석 중인 단일 실행 흐름에서 **"실제 어떤 비즈니스 사건이 발생하고, 어떤 규칙과 검증 관문을 거쳐 결과가 도출되는가"**를 설명하지 못한다.

최신 화면에서도 단순히 기술적 계층명(`presentation`, `usecase`)과 프로그래밍 식별자(`CheckoutPage.submit`, `CheckoutService.execute`)를 스토리보드에 1:1로 매핑함으로써, "기술 모듈 나열"이라는 과거의 한계를 벗어나지 못하고 있었다.
개발자가 복잡한 코드베이스에서 비즈니스 흐름과 코드 변경점(Surgery)을 1초 만에 인지할 수 있도록, 검증된 참조 모델(`live-semantic-map-final.html` 및 확정 디자인 스크린샷)을 기반으로 **비즈니스 관문 시퀀스(Gateway Sequence)** 중심의 Macro Context Storyboard 표준을 공식 승격해야 한다.

---

## 2. 결정 사항 (Decisions)

### 1) 패러다임 전환: 컴포넌트 구조도 ➔ 비즈니스 관문 시퀀스
- 스토리보드의 개별 프레임(Frame)은 소스 코드 파일이나 클래스 단위가 아닌, **비즈니스 여정의 핵심 관문(Business Gateway)**을 나타낸다.
- 기본 5개 표준 관문 역할(Role) 체계를 확립한다:
  1. `UI EVENT`: 사용자 터치/클릭 등 최초 이벤트 트리거
  2. `GATEWAY`: 사전 정합성 검증, 필터링, 정책 체크
  3. `DOMAIN CORE`: 도메인 불변식 검증, 자원 선점 및 핵심 규칙
  4. `APPLICATION`: 주문서 발급, 트랜잭션 조율, 상태 승격
  5. `EXTERNAL PG` (또는 `INFRASTRUCTURE`): 외부 결제망 연동 및 영속화 완료

### 2) 스토리보드 전용 뷰모델(`StoryFrame`) 계약 도입
- `Step`의 단순 속성 나열을 지양하고, 관문 중심의 고차 뷰모델 계약을 정의한다:
  - `frameOrdinal`: 관문 순서 (`FRAME 01` ~ `FRAME 05`)
  - `gatewayRole`: 비즈니스 관문 역할 (`UI EVENT`, `GATEWAY`, `DOMAIN CORE` 등)
  - `businessTitle`: 비즈니스 목적 및 사건명 (예: `고객 결제 버튼 클릭`, `실시간 재고 검증 & 선점`)
  - `businessNarrative`: 1~2줄의 비즈니스 행동 내러티브 (예: `품목 잔여량 확인, 10분간 선점 락 부여 및 토큰 발행`)
  - `technicalAnchor`: 실행 심볼 (예: `InventoryService.checkStock()`)
  - `surgeryBadge`: `ACTIVE SURGERY` 배지 (현재 수정 중인 단계 표시)
  - `deltaTag`: `+ NEW SURGERY`, `~ RULE CHG`, `- REMOVED` 변경 태그
  - `isHighlighted`: `⚡` 번개 아이콘 표시

### 3) 스크린샷 기준 네오-브루탈리즘 레이아웃 규격 고정
- **컨테이너**: `border: 2px solid var(--ink); border-radius: 9px; box-shadow: 3px 3px 0 var(--soft);`
- **헤더**: `2. MACRO CONTEXT STORYBOARD · 전체 비즈니스 흐름 조망 (5개 관문 엔드투엔드 시퀀스)` 및 우측 상태 알약 캡슐(`+1 Step Added · 1 Rule Modified`)
- **트랙**: `display: grid; grid-template-columns: repeat(5, 1fr); gap: 10px;` (전체 가로폭을 시원하게 채우는 5개 관문 균등 분할)
- **활성/수술 관문**:
  - 선택 카드: `border: 2px solid var(--ink); box-shadow: 2px 2px 0 var(--ink);`
  - 수술(Active Surgery) 카드: 상단 우측에 `ACTIVE SURGERY` 블랙 필과 `+ NEW SURGERY` 태그 장착, 제목 및 푸터에 `⚡` 번개 표시
- **구분선**: 본문 설명과 하단 기술 심볼 사이에 점선(`border-top: 1px dashed var(--line);`) 배치

### 4) Anti-Telemetry Guard 준수
- 내부 컴파일러의 에포크, 정착 지연 ms, 미확인 통계 등 내부 텔레메트리 누출을 금지하고, 비즈니스 관문과 검증 가능한 소스 코드만 표현한다.

---

## 3. 대안 검토 및 기각 이유 (Alternatives Considered)

- **대안 A: 기존의 기술 계층 레이블(`presentation`, `usecase`, `domain`) 유지**
  - 기각 이유: 개발자가 코드를 보지 않고도 비즈니스 요구사항을 직관적으로 이해할 수 있는 스토리보드의 본래 목적을 훼손함.
- **대안 B: 가로 스크롤 카드 나열 방식 유지**
  - 기각 이유: 화면 전체 폭을 활용하지 못하고 좌측으로 치우치며, 스크롤 박스에 의해 상단 배지가 잘리는 등의 시각적 완성도 저하가 지속됨.

---

## 4. 기대 효과 (Consequences)

1. 개발자가 시스템의 전체 흐름을 1초 만에 비즈니스 관문 순서로 인지 가능
2. 수정/변경(Surgery)이 발생한 비즈니스 위치를 직관적으로 파악
3. 디자인 스크린샷과 완벽하게 일치하는 일관된 UX 제공
