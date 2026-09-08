// Command flowmeter is the dedicated CodeFlow measurement routine.
//
// `flowmeter run`은 도구에 포함된 고정 benchmark project를 실행해 현재 빌드의
// 성능을 측정합니다. `flowmeter compare`는 같은 project와 실행 환경에서 생성된
// 최근 두 결과만 비교합니다. `flowmeter release collect`는 release 검증 명령과
// 로그 digest를 보존하며, 사람의 품질 기준 승인은 별도 단계로 유지합니다.
package main

import (
	"context"
	"os"

	"codeflow/internal/flowmeter"
)

func main() {
	os.Exit(flowmeter.Run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
