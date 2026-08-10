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

// queryArea fetches detail for a single area by UUID.
//
// Both climbs and children are selected because an area has one or the other,
// not both: non-leaf areas return climbs: [] regardless of totalClimbs, and the
// climbs live on their leaf descendants. Stawamus Chief reports totalClimbs: 369
// with climbs: [] and 32 children.
const queryArea = `
query GetArea($uuid: ID) {
  area(uuid: $uuid) {
    uuid
    areaName
    totalClimbs
    gradeContext
    pathTokens
    metadata {
      lat
      lng
      leaf
      isBoulder
    }
    content {
      description
    }
    children {
      uuid
      areaName
      totalClimbs
      metadata {
        lat
        lng
        leaf
      }
    }
    climbs {
      uuid
      name
      fa
      length
      safety
      type {
        sport
        trad
        bouldering
        alpine
        aid
        tr
        ice
        mixed
        snow
        deepwatersolo
      }
      grades {
        yds
        vscale
        font
        french
        uiaa
        ewbank
        wi
      }
    }
  }
}`
