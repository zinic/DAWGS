package ci

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func TestWorkflowsAreValidYAML(t *testing.T) {
	for _, name := range []string{"go-test.yml", "upload-coverage-files.yml"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join("..", "..", ".github", "workflows", name)
			content, err := os.ReadFile(path)
			require.NoError(t, err)
			var document yaml.Node
			require.NoError(t, yaml.Unmarshal(content, &document))
			require.Equal(t, yaml.DocumentNode, document.Kind)
			require.NotEmpty(t, document.Content)
		})
	}
}
