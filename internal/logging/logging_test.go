package logging

import (
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewWritesJSONToStandardOutput(t *testing.T) {
	dir := t.TempDir()
	cwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	defer func() { require.NoError(t, os.Chdir(cwd)) }()

	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	originalStdout := os.Stdout
	os.Stdout = writer
	defer func() { os.Stdout = originalStdout }()

	logger, err := New("agent")
	require.NoError(t, err)
	logger.Info("hello", "release", "test-release")
	require.NoError(t, writer.Close())
	output, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Contains(t, string(output), `"msg":"hello"`)
	require.Contains(t, string(output), `"release":"test-release"`)

	_, statErr := os.Stat("log")
	require.ErrorIs(t, statErr, os.ErrNotExist)
}
