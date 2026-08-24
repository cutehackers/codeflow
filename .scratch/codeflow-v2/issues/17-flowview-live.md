# 17: FlowView 실데이터 연결 + 코드 렌즈

**What to build:** 픽스처를 걷어내고 publish.pointer가 가리키는 latest generation을 소비한다. watch 자동 갱신(재발행 시 새로고침 없이 반영), 코드 렌즈 인라인 5~20줄 전개, remote 존재 시 GitHub permalink, stale 배지 표시.

**Blocked by:** 09 (publish 원자성), 16 (FlowView 셸).

**Status:** ready-for-agent

- [ ] republish 후 FlowView가 자동 갱신됨
- [ ] 코드 렌즈가 앵커 위치에 정확히 전개됨
- [ ] permalink 생성(remote 없으면 생략)
- [ ] stale 항목 배지 표시
