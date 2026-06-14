package execution

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReactorPythonLambdaFeedbackConfigUsesSimpleDefaults(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "evaluation_function"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "evaluation_function", "preview.py"), []byte("def preview_function():\n    return None\n"), 0o644))

	t.Setenv("FUNCTION_LF_ROOT", root)

	cfg, packageMode, err := reactorPythonLambdaFeedbackConfig()
	require.NoError(t, err)
	assert.True(t, packageMode)
	assert.Equal(t, root, cfg.Root)
	assert.Equal(t, "evaluation_function.evaluation:evaluation_function", cfg.EvalEntrypoint)
	assert.Equal(t, "evaluation_function.preview:preview_function", cfg.PreviewEntrypoint)
	assert.Equal(t, "examples/lambda-feedback-adapter", cfg.AdapterRoot)
	assert.Equal(t, "tools/lf-bundle-python/lf_bundle_python.py", cfg.Bundler)
	assert.Equal(t, "python3", cfg.Python)
	assert.Empty(t, cfg.Out)
	assert.Empty(t, cfg.IncludeRoots)
	assert.Empty(t, cfg.SysPath)
}
