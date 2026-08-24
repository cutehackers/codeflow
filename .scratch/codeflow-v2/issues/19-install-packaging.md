# 19: 설치 패키징 — Homebrew + Releases + doctor

**What to build:** "사용자 명령 1개, 런타임 의존 0, 첫 실행 30초" 실현. Homebrew tap formula, GitHub Releases 멀티플랫폼 바이너리(FlowView asset embed), 어댑터 pin 버전 자동 다운로드, `codeflow doctor` 진단, curl 스크립트 보조 경로.

**Blocked by:** 04 (init/pin), 06 (어댑터 다운로드 대상), 16 (embed할 FlowView).

**Status:** ready-for-agent

- [ ] 깨끗한 macOS에서 brew 설치 → init → 30초 내 첫 성공
- [ ] 어댑터가 pin 버전으로 자동 다운로드됨
- [ ] doctor가 미비점을 보고함
