package main

import (
	"os"
	"path/filepath"

	"github.com/jonhill90/agent-tui/internal/secrets"
)

// secretsSchemaRelPath is where hill90-app keeps the file Secrets renders,
// relative to that repo's root -- tracked in git there (the file's own
// doc comment: "Reconstructed during the local-stack work (JON-38)... It
// is read at request time by services/api/src/routes/secrets.ts to
// render /admin/secrets").
const secretsSchemaRelPath = "platform/vault/secrets-schema.yaml"

// resolveSecretsSchema turns the -secrets-schema flag and $HILL90_APP_REPO
// into a readable path, or "" when neither yields one -- the same shape,
// and the same reasoning, as resolveOpenAPISpec (docs.go) uses for its own
// hill90-app file. An explicit -secrets-schema that does not exist is
// returned as given, not silently downgraded to "", so the pane reports
// the path it could not read rather than claiming nothing was configured.
func resolveSecretsSchema(flagPath, appRepo string) string {
	if flagPath != "" {
		return flagPath
	}
	if appRepo == "" {
		return ""
	}
	candidate := filepath.Join(appRepo, secretsSchemaRelPath)
	if _, err := os.Stat(candidate); err != nil {
		return candidate
	}
	return candidate
}

// buildSecretsFetch returns nil -- secrets.New's own "unconfigured" state
// -- when no schema path was resolvable at all.
func buildSecretsFetch(schemaPath string) secrets.Fetcher {
	if schemaPath == "" {
		return nil
	}
	return secrets.NewFetcher(schemaPath)
}
