// Package naming implements the deterministic derived naming engine (design §8.1,
// ticket 11).
//
// Converts programming identifiers (verbs, nouns, handlers) into natural language
// business step titles (Korean and English) without any LLMs or non-deterministic guessing.
package naming

import (
	"strings"
	"unicode"
)

// VerbMap translates common business action verbs (and inflections) into Korean verb endings.
var verbMap = map[string]string{
	"submit":    "제출한다",
	"submits":   "제출한다",
	"submitted": "제출한다",
	"send":      "전송한다",
	"sends":     "전송한다",
	"sent":      "전송한다",
	"create":    "생성한다",
	"creates":   "생성한다",
	"created":   "생성한다",
	"save":      "저장한다",
	"saves":     "저장한다",
	"saved":     "저장한다",
	"update":    "갱신한다",
	"updates":   "갱신한다",
	"updated":   "갱신한다",
	"delete":    "삭제한다",
	"deleted":   "삭제한다",
	"remove":    "제거한다",
	"removed":   "제거한다",
	"load":      "불러온다",
	"loaded":    "불러온다",
	"fetch":     "가져온다",
	"fetched":   "가져온다",
	"refresh":   "새로고침한다",
	"refreshed": "새로고침한다",
	"check":     "검사한다",
	"checked":   "검사한다",
	"validate":   "검증한다",
	"verify":     "인증한다",
	"verified":   "인증한다",
	"verification": "인증한다",
	"auth":       "인증한다",
	"login":      "로그인한다",
	"logout":     "로그아웃한다",
	"signup":     "회원가입한다",
	"register":   "가입한다",
	"registers":  "가입한다",
	"registered": "가입한다",
	"checkout":  "결제를 시작한다",
	"pay":       "결제한다",
	"paid":      "결제한다",
	"place":     "접수한다",
	"placed":    "접수한다",
	"order":     "주문한다",
	"ordered":   "주문한다",
	"cancel":    "취소한다",
	"canceled":  "취소한다",
	"handle":    "처리한다",
	"handled":   "처리한다",
	"open":      "연다",
	"opened":    "연다",
	"close":     "닫는다",
	"closed":    "닫는다",
	"navigate":  "이동한다",
	"route":     "라우팅한다",
	"push":      "푸시한다",
	"pop":       "돌아간다",
	"add":       "추가한다",
	"adds":      "추가한다",
	"added":     "추가한다",
	"clear":     "비운다",
	"cleared":   "비운다",
	"reset":     "초기화한다",
	"execute":   "실행한다",
	"executed":  "실행한다",
	"call":      "호출한다",
	"called":    "호출한다",
}

// NounMap translates common domain nouns into Korean with appropriate particle.
var nounMap = map[string]string{
	"order":        "주문을",
	"orders":       "주문 목록을",
	"item":         "아이템을",
	"items":        "아이템 목록을",
	"cart":         "장바구니를",
	"user":         "사용자를",
	"users":        "사용자 목록을",
	"email":        "이메일을",
	"password":     "비밀번호를",
	"auth":         "인증 정보를",
	"token":        "토큰을",
	"session":      "세션을",
	"receipt":      "영수증을",
	"payment":      "결제 정보를",
	"profile":      "프로필을",
	"deeplink":     "딥링크를",
	"link":         "링크를",
	"message":      "메시지를",
	"notification": "알림을",
	"status":       "상태를",
	"data":         "데이터를",
	"signup":       "회원가입을",
	"registration": "가입을",
	"account":      "계정을",
	"verify":       "인증을",
	"verification": "인증을",
}

// SplitWords splits camelCase, PascalCase, snake_case, and digits into individual lower-cased words.
func SplitWords(ident string) []string {
	ident = strings.TrimPrefix(ident, "_")
	if strings.HasPrefix(ident, "on") && len(ident) > 2 && unicode.IsUpper(rune(ident[2])) {
		ident = ident[2:]
	}
	if strings.HasSuffix(ident, "Pressed") || strings.HasSuffix(ident, "Tapped") || strings.HasSuffix(ident, "Clicked") {
		ident = strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(ident, "Pressed"), "Tapped"), "Clicked")
	}

	var words []string
	var cur strings.Builder

	flush := func() {
		if cur.Len() > 0 {
			words = append(words, strings.ToLower(cur.String()))
			cur.Reset()
		}
	}

	runes := []rune(ident)
	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		if ch == '_' || ch == '$' || ch == '-' {
			flush()
			continue
		}
		if unicode.IsDigit(ch) {
			if cur.Len() > 0 && !unicode.IsDigit(runes[i-1]) {
				flush()
			}
		} else if unicode.IsUpper(ch) {
			afterLowerOrDigit := cur.Len() > 0 && (unicode.IsLower(runes[i-1]) || unicode.IsDigit(runes[i-1]))
			acronymEnd := cur.Len() >= 2 && unicode.IsUpper(runes[i-1]) && (i+1 < len(runes) && unicode.IsLower(runes[i+1]))
			if afterLowerOrDigit || acronymEnd {
				flush()
			}
		} else if unicode.IsLower(ch) {
			if cur.Len() > 0 && unicode.IsDigit(runes[i-1]) {
				flush()
			}
		}
		cur.WriteRune(ch)
	}
	flush()
	return words
}

// DeriveTitle derives a natural language description (Korean preferred, fallback to English)
// for a method or symbol identifier.
func DeriveTitle(symbolName string) string {
	rawWords := SplitWords(symbolName)
	if len(rawWords) == 0 {
		return "알 수 없는 단계"
	}

	// Normalize combined phrases like ["deep", "link"] -> ["deeplink"], ["sign", "up"] -> ["signup"]
	var words []string
	for i := 0; i < len(rawWords); i++ {
		w := rawWords[i]
		if i+1 < len(rawWords) {
			combined := w + rawWords[i+1]
			if combined == "deeplink" || combined == "signup" || combined == "login" || combined == "checkout" {
				words = append(words, combined)
				i++
				continue
			}
		}
		words = append(words, w)
	}

	// Try verb + noun or noun + verb in Korean
	var foundVerbKo string
	var foundNounKo string
	var verbWord string

	for _, w := range words {
		if ko, ok := verbMap[w]; ok && foundVerbKo == "" {
			foundVerbKo = ko
			verbWord = w
		}
	}
	for _, w := range words {
		if ko, ok := nounMap[w]; ok && foundNounKo == "" {
			// Avoid redundant "회원가입을 회원가입한다" when the same word
			// is both verb and noun (e.g. signup). Prefer a distinct noun
			// if available – keeps DeriveTitle("emailSignup") → "이메일을 회원가입한다"
			// and DeriveTitle("signUpWithEmail") stable after adding signup to nounMap.
			if w == verbWord {
				continue
			}
			foundNounKo = ko
		}
	}
	// Fallback: if verbWord itself is the only noun candidate, it was skipped
	// above; we still want noun+verb only when noun is distinct. So verb-only
	// is correct for bare "signup".

	if foundVerbKo != "" && foundNounKo != "" {
		return foundNounKo + " " + foundVerbKo
	}
	if foundVerbKo != "" {
		return foundVerbKo
	}

	// English fallback: Title case words joined
	englishWords := make([]string, len(rawWords))
	for i, w := range rawWords {
		if len(w) > 0 {
			englishWords[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(englishWords, " ")
}
