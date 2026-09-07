// Command flowmeter is the dedicated CodeFlow measurement routine.
//
// `flowmeter run`은 도구에 포함된 고정 benchmark project를 실행해 현재 빌드의
// 성능을 측정합니다. `flowmeter compare`는 같은 project와 실행 환경에서 생성된
// 최근 두 결과만 비교합니다. 별도 project나 설정 파일은 필요하지 않습니다.
package main

import (
	"context"
	"os"

	"codeflow/internal/flowmeter"
)

func main() {
	os.Exit(flowmeter.Run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
