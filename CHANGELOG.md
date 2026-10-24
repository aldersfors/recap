# Changelog

## [0.5.0](https://github.com/aldersfors/recap/compare/v0.4.1...v0.5.0) (2026-10-24)


### Features

* **summarize:** add claude-code provider ([#42](https://github.com/aldersfors/recap/issues/42)) ([e36f8e1](https://github.com/aldersfors/recap/commit/e36f8e1426e86330ce2bef3ba53e1f6ef4f85aa8))

## [0.4.1](https://github.com/aldersfors/recap/compare/v0.4.0...v0.4.1) (2026-10-21)


### Bug Fixes

* **deps:** update module github.com/anthropics/anthropic-sdk-go to v1.82.0 ([#38](https://github.com/aldersfors/recap/issues/38)) ([c399972](https://github.com/aldersfors/recap/commit/c399972dda98d715aa8de97935c1b0ef3a5e7549))

## [0.4.0](https://github.com/aldersfors/recap/compare/v0.3.3...v0.4.0) (2026-10-20)


### Features

* **activity:** add Plane as ticket and activity source ([#37](https://github.com/aldersfors/recap/issues/37)) ([4eb672e](https://github.com/aldersfors/recap/commit/4eb672e195efcc7d8c4a3c306806f9e81b87bf9c))


### Bug Fixes

* **deps:** update module github.com/anthropics/anthropic-sdk-go to v1.81.0 ([#30](https://github.com/aldersfors/recap/issues/30)) ([425dead](https://github.com/aldersfors/recap/commit/425dead65cce60aa19ad44941c527552b5600cd9))

## [0.3.3](https://github.com/aldersfors/recap/compare/v0.3.2...v0.3.3) (2026-10-15)


### Bug Fixes

* **deps:** update aws-sdk-go-v2 monorepo ([#24](https://github.com/aldersfors/recap/issues/24)) ([fd30514](https://github.com/aldersfors/recap/commit/fd3051477b1ac12bfa2e28975597cbb34dfa384d))
* **deps:** update aws-sdk-go-v2 monorepo ([#27](https://github.com/aldersfors/recap/issues/27)) ([71ffc9f](https://github.com/aldersfors/recap/commit/71ffc9f0ebf513e4879c3fcaa9fb999f426c1a39))
* **deps:** update module github.com/anthropics/anthropic-sdk-go to v1.79.0 ([#20](https://github.com/aldersfors/recap/issues/20)) ([16caf93](https://github.com/aldersfors/recap/commit/16caf93d45d31dd6edf4a3b568154a68b5e7adc7))
* **deps:** update module github.com/anthropics/anthropic-sdk-go to v1.79.1 ([#22](https://github.com/aldersfors/recap/issues/22)) ([7fadc9c](https://github.com/aldersfors/recap/commit/7fadc9c693d8dee1312f61f02081ddaa061e28f4))
* **deps:** update module github.com/anthropics/anthropic-sdk-go to v1.80.0 ([#26](https://github.com/aldersfors/recap/issues/26)) ([5d0e063](https://github.com/aldersfors/recap/commit/5d0e0639b49df635c515019ddec191361d3635f8))

## [0.3.2](https://github.com/aldersfors/recap/compare/v0.3.1...v0.3.2) (2026-10-11)


### Bug Fixes

* **deps:** migrate render to goldmark v2 ([#19](https://github.com/aldersfors/recap/issues/19)) ([912063a](https://github.com/aldersfors/recap/commit/912063a301e132f3cf2aa82315d9fea69ef845c8))
* **deps:** update module github.com/anthropics/anthropic-sdk-go to v1.78.0 ([#14](https://github.com/aldersfors/recap/issues/14)) ([593ecfa](https://github.com/aldersfors/recap/commit/593ecfaaea55785d0bc9e5c8df7edac5cc212c22))
* **deps:** update module github.com/yuin/goldmark to v2 ([#17](https://github.com/aldersfors/recap/issues/17)) ([56fc7d2](https://github.com/aldersfors/recap/commit/56fc7d23f340a8b700ad2c4525c8987deefc14bd))

## [0.3.1](https://github.com/aldersfors/recap/compare/v0.3.0...v0.3.1) (2026-10-09)


### Bug Fixes

* **release:** publish assets before the release goes immutable ([#14](https://github.com/aldersfors/recap/issues/14)) ([0cf86f5](https://github.com/aldersfors/recap/commit/0cf86f5e8d245be0e45684f864a4b4d1981b379f))

## [0.3.0](https://github.com/aldersfors/recap/compare/v0.2.0...c228b27915da0a3835c8ca13a9f0590317df7138) (2026-10-09)


### Features

* **render:** embed email images and write an .eml draft ([d7df804](https://github.com/aldersfors/recap/commit/d7df804b56b265b12068e07e2d6f86be67196aba))
* **summarize:** add Bedrock Converse provider ([a49066f](https://github.com/aldersfors/recap/commit/a49066ff1eb99fe3555753c39fee7b773b834b70))
* **summarize:** group the week's activity by epic and scope ([adbc5f6](https://github.com/aldersfors/recap/commit/adbc5f6032522f22702b0b6c3017fed1f57272f9))

## [0.2.0](https://github.com/aldersfors/recap/compare/v0.1.0...v0.2.0) (2026-09-26)


### ⚠ BREAKING CHANGES

* **cli:** single-dash long flags such as -week are no longer accepted. Use the double-dash form, e.g. --week 2026-W39.

### Features

* **cli:** move command parsing to Kong ([4cc643f](https://github.com/aldersfors/recap/commit/4cc643f5b1a75ebf0ab5519f6c7177191261e597))
