package openbeta

// Regenerate the typed GraphQL client from schema/openbeta.graphql and the
// operations in queries/. The generator version is pinned by tools.go, so this
// is reproducible.
//
//	go generate ./internal/openbeta/
//
// genqlient.yaml lives at the repository root and its paths are relative to it,
// but a directive runs in the directory of the file holding it — hence the
// explicit config path.
//
//go:generate go run github.com/Khan/genqlient ../../genqlient.yaml
