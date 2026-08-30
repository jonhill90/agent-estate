package navwalk

import (
	"encoding/json"
	"fmt"
	"os"
)

// ManifestEntry is one nav destination's own identity for this package's
// purposes -- ID matches internal/nav.Item.ID exactly (the same string
// internal/shell's routeToPane map keys by), so a route's storage file
// name is never invented separately from the route itself. Label is the
// display name Generate puts in the table's own "Destination" column.
type ManifestEntry struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// ReadManifest loads path's ordered destination list -- the ONE file in
// this whole scheme that lists every route (manifest.json), separate from
// any lane's own per-route measurement. It changes only when
// internal/nav.Build()'s tree itself gains, loses, or renames a
// destination -- a genuinely rare, structural event a human should
// actually review, unlike the per-route measurement files this scheme
// exists to keep out of everyone's way. A manifest conflict is a real
// nav-tree conflict, not a false one two unrelated measurements created.
func ReadManifest(path string) ([]ManifestEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("navwalk: read manifest %s: %w", path, err)
	}
	var entries []ManifestEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("navwalk: decode manifest %s: %w", path, err)
	}
	return entries, nil
}
