package openbeta

// GraphQL query documents for the OpenBeta API.
//
// Field selections here were confirmed by introspection and live queries against
// https://api.openbeta.io/graphql. See docs/graphql-findings.md for the evidence
// behind the less obvious choices.
//
// The operations in queries/ are compiled to a typed client in generated/ by
// genqlient. Regenerate after editing a query or refreshing the schema; the
// result is committed and CI checks it is up to date.
//
//go:generate go run github.com/Khan/genqlient ../../genqlient.yaml

// queryCragsWithin fetches areas inside a bounding box.
//
// climbs { uuid } is requested purely to count real climbs. The Area.totalClimbs
// field cannot be used for that: it reads 0 on the majority of leaf crags that
// demonstrably have climbs (Tantalus Wall reports 0 with 8 climbs). uuid is the
// cheapest field on Climb that is guaranteed non-null.
const queryCragsWithin = `
query CragsWithin($filter: SearchWithinFilter) {
  cragsWithin(filter: $filter) {
    uuid
    areaName
    totalClimbs
    pathTokens
    metadata {
      lat
      lng
      leaf
      isBoulder
    }
    climbs {
      uuid
    }
  }
}`
