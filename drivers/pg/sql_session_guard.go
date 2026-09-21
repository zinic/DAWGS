package pg

import "strings"

type sqlTokenKind uint8

const (
	sqlWord sqlTokenKind = iota
	sqlString
	sqlSymbol
)

type sqlToken struct {
	kind  sqlTokenKind
	value string
}

func isSessionIdentityStatement(statement string) bool {
	tokens := tokenizeSQL(statement)
	for index, token := range tokens {
		if token.kind != sqlWord {
			continue
		}
		switch token.value {
		case "do":
			// Anonymous procedural blocks cannot be classified safely without a
			// PostgreSQL parser and are unnecessary for application operations.
			if sqlStatementStart(tokens, index) {
				return true
			}
		case "discard":
			if tokenWord(tokens, index+1) == "all" {
				return true
			}
		case "reset":
			if !sqlStatementStart(tokens, index) {
				continue
			}
			next := tokenWord(tokens, index+1)
			if next == "role" || next == "session_authorization" ||
				(next == "session" && tokenWord(tokens, index+2) == "authorization") {
				return true
			}
			return true
		case "set":
			if !sqlStatementStart(tokens, index) {
				continue
			}
			nextIndex := index + 1
			next := tokenWord(tokens, nextIndex)
			local := next == "local"
			if local || next == "session" {
				if next == "session" && tokenWord(tokens, nextIndex+1) == "authorization" {
					return true
				}
				nextIndex++
				next = tokenWord(tokens, nextIndex)
			}
			if next == "role" || next == "session_authorization" ||
				(next == "session" && tokenWord(tokens, nextIndex+1) == "authorization") {
				return true
			}
			if !local && next != "transaction" && next != "constraints" {
				// Persistent session settings can poison a pooled backend. SET
				// LOCAL remains transaction-scoped and is safe for non-identity
				// settings.
				return true
			}
		case "set_config":
			if index+1 >= len(tokens) || tokens[index+1].kind != sqlSymbol || tokens[index+1].value != "(" {
				continue
			}
			return true
		case "create":
			if sqlStatementStart(tokens, index) {
				next := tokenWord(tokens, index+1)
				if next == "temp" || next == "temporary" {
					return true
				}
			}
		}
	}
	return false
}

func sqlStatementStart(tokens []sqlToken, index int) bool {
	return index == 0 || tokens[index-1].kind == sqlSymbol && tokens[index-1].value == ";"
}

func tokenWord(tokens []sqlToken, index int) string {
	if index < 0 || index >= len(tokens) || tokens[index].kind != sqlWord {
		return ""
	}
	return tokens[index].value
}

func tokenizeSQL(statement string) []sqlToken {
	var tokens []sqlToken
	for index := 0; index < len(statement); {
		switch {
		case isSQLSpace(statement[index]):
			index++
		case index+1 < len(statement) && statement[index:index+2] == "--":
			index += 2
			for index < len(statement) && statement[index] != '\n' {
				index++
			}
		case index+1 < len(statement) && statement[index:index+2] == "/*":
			index = skipSQLBlockComment(statement, index)
		case statement[index] == '\'':
			value, next := scanSQLQuoted(statement, index, '\'')
			tokens = append(tokens, sqlToken{kind: sqlString, value: value})
			index = next
		case statement[index] == '"':
			value, next := scanSQLQuoted(statement, index, '"')
			tokens = append(tokens, sqlToken{kind: sqlWord, value: strings.ToLower(value)})
			index = next
		case statement[index] == '$':
			if next, ok := skipDollarQuoted(statement, index); ok {
				tokens = append(tokens, sqlToken{kind: sqlString})
				index = next
			} else {
				tokens = append(tokens, sqlToken{kind: sqlSymbol, value: string(statement[index])})
				index++
			}
		case isSQLWordStart(statement[index]):
			start := index
			for index < len(statement) && isSQLWordPart(statement[index]) {
				index++
			}
			tokens = append(tokens, sqlToken{kind: sqlWord, value: strings.ToLower(statement[start:index])})
		default:
			tokens = append(tokens, sqlToken{kind: sqlSymbol, value: string(statement[index])})
			index++
		}
	}
	return tokens
}

func skipSQLBlockComment(statement string, index int) int {
	depth := 0
	for index < len(statement) {
		if index+1 < len(statement) && statement[index:index+2] == "/*" {
			depth++
			index += 2
			continue
		}
		if index+1 < len(statement) && statement[index:index+2] == "*/" {
			depth--
			index += 2
			if depth == 0 {
				return index
			}
			continue
		}
		index++
	}
	return index
}

func scanSQLQuoted(statement string, index int, quote byte) (string, int) {
	index++
	var value strings.Builder
	for index < len(statement) {
		if statement[index] == quote {
			if index+1 < len(statement) && statement[index+1] == quote {
				value.WriteByte(quote)
				index += 2
				continue
			}
			return value.String(), index + 1
		}
		value.WriteByte(statement[index])
		index++
	}
	return value.String(), index
}

func skipDollarQuoted(statement string, index int) (int, bool) {
	tagEnd := index + 1
	for tagEnd < len(statement) && (isSQLWordPart(statement[tagEnd]) || statement[tagEnd] == '$') {
		if statement[tagEnd] == '$' {
			tag := statement[index : tagEnd+1]
			if end := strings.Index(statement[tagEnd+1:], tag); end >= 0 {
				return tagEnd + 1 + end + len(tag), true
			}
			return len(statement), true
		}
		tagEnd++
	}
	return index, false
}

func isSQLSpace(value byte) bool {
	return value == ' ' || value == '\t' || value == '\r' || value == '\n' || value == '\f'
}

func isSQLWordStart(value byte) bool {
	return value == '_' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= 0x80
}

func isSQLWordPart(value byte) bool {
	return isSQLWordStart(value) || value >= '0' && value <= '9' || value == '$'
}
