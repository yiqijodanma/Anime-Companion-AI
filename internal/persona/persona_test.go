package persona

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSystemPromptFixed(t *testing.T) {
	require.NotEmpty(t, SystemPrompt)
	require.True(t, strings.Contains(SystemPrompt, "凉宫春日"))
	require.Equal(t, "晚安啦！", GoodNight)
}
