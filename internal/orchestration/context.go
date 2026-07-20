package orchestration

import (
	"strings"
	"unicode"
)

const defaultContextTokenBudget = 6000

func selectHistoryBatches(history []Message, characterPrompt, currentUser string, prefix []Message, summaries []string) []Message {
	remaining := defaultContextTokenBudget - 1200 - estimateTokens(characterPrompt) - estimateTokens(currentUser)
	for _, summary := range summaries {
		remaining -= estimateTokens(summary)
	}
	for _, message := range prefix {
		remaining -= estimateMessageTokens(message)
	}
	if remaining <= 0 || len(history) == 0 {
		return nil
	}

	type batchRange struct{ start, end, tokens int }
	batches := make([]batchRange, 0)
	for start := 0; start < len(history); {
		end := start + 1
		batchID := history[start].BatchID
		for end < len(history) && batchID != "" && history[end].BatchID == batchID {
			end++
		}
		tokens := 0
		for _, message := range history[start:end] {
			tokens += estimateMessageTokens(message)
		}
		batches = append(batches, batchRange{start: start, end: end, tokens: tokens})
		start = end
	}
	first := len(history)
	for i := len(batches) - 1; i >= 0; i-- {
		if batches[i].tokens > remaining {
			break
		}
		remaining -= batches[i].tokens
		first = batches[i].start
	}
	return append([]Message(nil), history[first:]...)
}

func estimateMessageTokens(message Message) int {
	return 8 + estimateTokens(message.DisplayName) + estimateTokens(message.SpeakerID) + estimateTokens(message.Content)
}

func estimateTokens(value string) int {
	nonASCII := 0
	asciiWords := 0
	inWord := false
	for _, r := range value {
		if r > unicode.MaxASCII {
			nonASCII++
			inWord = false
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if !inWord {
				asciiWords++
				inWord = true
			}
		} else {
			inWord = false
		}
	}
	return nonASCII + asciiWords + (len(strings.TrimSpace(value)) / 16)
}
