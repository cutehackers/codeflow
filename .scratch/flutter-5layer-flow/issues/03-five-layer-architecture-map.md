# 03: 5레인 Architecture Map (page → controller → useCase → repository → api)

**What to build:** 회원가입 같은 비즈니스 흐름을 열면 `화면(Page) → 컨트롤러(Controller) → 유스케이스(UseCase) → 리포지토리(Repository) → API(ApiClient)` 5레인이 고정 순서로 보이고, 프로젝트 어휘(`presentation/controller/usecase/repository/datasource`)에 따라 레인 라벨이 적응되며, `ref.read(provider)(args)` 경유로 `controller → useCase → repository`까지 단계가 각 레인에 배치된다.

**Blocked by:** 02: 비즈니스 흐름 탭 (큰 흐름만, 1~2줄 요약).

**Status:** done

- [x] `layers.go`가 파일 경로·심볼에서 5개 레이어를 추론하고 FlowView가 5레인을 고정 순서로 렌더링하며 빈 레인은 비어 있음으로 표시된다
- [x] `slice`가 `ref.read(provider)`를 `UseCase → Repository`로 depth 3까지 추적하고, 발행된 `flow-534`에 3개 이상 레이어의 단계가 포함된다
