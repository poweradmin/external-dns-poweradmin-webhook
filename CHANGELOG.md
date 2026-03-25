# Changelog

## [1.2.1](https://github.com/poweradmin/external-dns-poweradmin-webhook/compare/v1.2.0...v1.2.1) (2026-03-25)


### Bug Fixes

* use pre-built binary in GoReleaser Docker build to fix missing go.sum ([a843a74](https://github.com/poweradmin/external-dns-poweradmin-webhook/commit/a843a747dc8db677d0c269e667b1830eb4f18537))

## [1.2.0](https://github.com/poweradmin/external-dns-poweradmin-webhook/compare/v1.1.2...v1.2.0) (2026-03-25)


### Features

* support PowerAdmin v2 wrapped API response format for records list ([74a8189](https://github.com/poweradmin/external-dns-poweradmin-webhook/commit/74a8189350b96639b3f0eb43b9517830da0d914a))

## [1.1.2](https://github.com/poweradmin/external-dns-poweradmin-webhook/compare/v1.1.1...v1.1.2) (2026-03-13)


### Bug Fixes

* keep .git/ in Docker build context for version metadata ([8a576fb](https://github.com/poweradmin/external-dns-poweradmin-webhook/commit/8a576fb6dfb8682ac5cd7b8858e38b5a838d9000))

## [1.1.1](https://github.com/poweradmin/external-dns-poweradmin-webhook/compare/v1.1.0...v1.1.1) (2026-03-06)


### Bug Fixes

* resolve FlexBool unmarshalling and TXT record quoting issues ([c7ebdde](https://github.com/poweradmin/external-dns-poweradmin-webhook/commit/c7ebddeba9832a0307c4b5f1049dd271c7259002))

## [1.1.0](https://github.com/poweradmin/external-dns-poweradmin-webhook/compare/v1.0.3...v1.1.0) (2025-12-06)


### Features

* enable backward compatibility with PowerAdmin API v1 ([abf2d5f](https://github.com/poweradmin/external-dns-poweradmin-webhook/commit/abf2d5f3d4f1555716d490e96555b53383aed5ad))

## [1.0.3](https://github.com/poweradmin/external-dns-poweradmin-plugin/compare/v1.0.2...v1.0.3) (2025-12-06)


### Bug Fixes

* switch to distroless base image for smaller footprint ([b50ad7f](https://github.com/poweradmin/external-dns-poweradmin-plugin/commit/b50ad7f84b3c4d78c7fdf5cd6d34e47827f9894c))

## [1.0.2](https://github.com/poweradmin/external-dns-poweradmin-plugin/compare/v1.0.1...v1.0.2) (2025-12-06)


### Bug Fixes

* add Dockerfile.goreleaser for GoReleaser builds ([eb1b9e8](https://github.com/poweradmin/external-dns-poweradmin-plugin/commit/eb1b9e8cd3ca6a86ed563b943be868d9c74617cf))

## [1.0.1](https://github.com/poweradmin/external-dns-poweradmin-plugin/compare/v1.0.0...v1.0.1) (2025-12-06)


### Bug Fixes

* add cmd/webhook/main.go and fix .gitignore patterns ([cf9408a](https://github.com/poweradmin/external-dns-poweradmin-plugin/commit/cf9408a3bfcc08dbefff570fad5dfc1b37c67d7b))

## 1.0.0 (2025-12-06)


### Features

* add external-dns webhook provider for PowerAdmin ([329ab96](https://github.com/poweradmin/external-dns-poweradmin-plugin/commit/329ab96a482c47e98a924b5bc76ca9477392ce37))


### Bug Fixes

* preserve target multiplicity in multi-target record updates ([fc297a1](https://github.com/poweradmin/external-dns-poweradmin-plugin/commit/fc297a1e8b607d2794d9fc202c1270bcc28a01d4))
* **provider:** match records by full DNS name instead of short name ([84bda25](https://github.com/poweradmin/external-dns-poweradmin-plugin/commit/84bda256dd34c539d87b3b2e9cc8104353d9093e))
* resolve multi-target update and domain filter issues ([6f0507b](https://github.com/poweradmin/external-dns-poweradmin-plugin/commit/6f0507bfab10fd27712e70a351821fb8c9661de0))
