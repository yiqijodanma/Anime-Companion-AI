package logging

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewCreatesLogFile(t *testing.T) {
	dir := t.TempDir()
	cwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	defer func() {
		require.NoError(t, os.Chdir(cwd))
	}()

	logger, err := New("agent")
	require.NoError(t, err)
	require.NotNil(t, logger)
	logger.Info("hello")

	_, statErr := os.Stat(filepath.Join(dir, "log", "agent.log"))
	require.NoError(t, statErr)
}
