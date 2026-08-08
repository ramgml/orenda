# Changelog

All notable changes to Orenda are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## Versioning policy

- **Branches:**
  - `main` — stable, tagged releases only. No direct commits.
  - `dev` — active development. All feature work lands here first.
  - `phase-X-Y-<name>` — feature branches off `dev` for individual tasks.
- **Tags:**
  - On `main`: `vX.Y.Z` — production releases.
  - On `dev`: `vX.Y.Z-phaseN` — phase milestones (e.g., `v0.1.0-phase1`).
- **Pre-1.0:** version is `0.MINOR.PATCH`. Anything may change between minors.
- **Source of truth:** `VERSION` file at repo root. `Makefile` reads it via `git describe`.

## [Unreleased]

### Added
- Initial project skeleton: `docs/PRD.md`, `docs/PLAN.md`, `AGENTS.md`, `opencode.json`, `Makefile`, `.gitignore`, `README.md`, `data/config.example.yaml`, `VERSION`.
- DB schema drafted in `docs/PLAN.md` (DDL pending Phase 1 migration).

### Changed

### Deprecated

### Removed

### Fixed

### Security

## [0.1.0] — TBD

Initial pre-alpha skeleton. No runtime code yet.