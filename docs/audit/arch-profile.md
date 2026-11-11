# Architecture Profile

Generated: 2026-11-11

Confidence: none — manual review required

## Detected Patterns

Detected: none

No catalogued pattern reaches Medium confidence. The evidence considered:

- Directory tree: the only Go package is the module root (`github.com/ivuorinen/paperboy`, `package main`). The other
  directories hold CI (`.github/`), audit output (`docs/`) and sample inputs (`examples/`); none carries a layer, port,
  feature or module name from the catalogue.
- Naming: two data types (`Config`, `Article`) and free functions named for their step (`loadConfig`, `loadTemplate`,
  `fetchArticles`, `itemsToArticles`, `generateMarkdown`, `mdText`, `mdURL`, `getURLDomain`). No `*Repository`,
  `*Service`, `*Port`, `*Adapter`, `*Handler` or similar suffixes.
- Import direction: one package importing the standard library plus `github.com/mmcdole/gofeed` and
  `go.yaml.in/yaml/v3`. There is no internal package graph to give a dependency direction.
- Pipe and Filter (Low confidence only): `run` performs a linear load → fetch → convert → group → render → write sequence,
  but there is no `pipeline/`, `filters/` or stage abstraction, so it is a procedural program, not the pattern.

## Detected Combination

none

## Inferred Structural Rules

none

## Ambiguities & Contradictions

none — a single-file command has no boundaries for signals to contradict.
