# Changelog

## [1.4.1](https://github.com/poweradmin/external-dns-poweradmin-webhook/compare/v1.4.0...v1.4.1) (2026-07-10)


### Bug Fixes

* reject record responses without a record object and cap response body reads ([5e5efef](https://github.com/poweradmin/external-dns-poweradmin-webhook/commit/5e5efef68d514ca84731e2d3eaf7d17450fc9a89))

## [1.4.0](https://github.com/poweradmin/external-dns-poweradmin-webhook/compare/v1.3.0...v1.4.0) (2026-07-09)


### Bug Fixes

* accept string and null disabled values, verify delete responses, trim base URL slash ([0dd36d5](https://github.com/poweradmin/external-dns-poweradmin-webhook/commit/0dd36d52555f24ff772b9e2efedcb9cc94b1caef))
* aggregate multi-target record sets and reconcile target changes on update ([9a9e162](https://github.com/poweradmin/external-dns-poweradmin-webhook/commit/9a9e16205e19937936fb17f8626cacad22e2d69e))
* decode record IDs as numbers or strings so agent-backend PowerAdmin deployments can sync ([bdd4b08](https://github.com/poweradmin/external-dns-poweradmin-webhook/commit/bdd4b08181f3652a484b0676540d5d813ed7e279))
* decode v1 create response disabled field with FlexBool ([ac516b4](https://github.com/poweradmin/external-dns-poweradmin-webhook/commit/ac516b4e8657315f155ea753a54db487f8a1f227))
* drop unsupported and apex NS endpoints and trim CNAME targets in AdjustEndpoints ([e59c5e6](https://github.com/poweradmin/external-dns-poweradmin-webhook/commit/e59c5e67befe168a3fcb4834d0f5c15688d73d1d))
* expose only filter-matching records when a parent zone is admitted ([25afe03](https://github.com/poweradmin/external-dns-poweradmin-webhook/commit/25afe03e61174708ac9b21f157ab292f0af962ca))
* expose unconfigured TTL when a record set has mixed TTLs so drift is repaired ([250bf78](https://github.com/poweradmin/external-dns-poweradmin-webhook/commit/250bf78ef107613adec709635d27fcb6586e1561))
* fail Records when a zone listing errors instead of returning a partial view ([9cd280f](https://github.com/poweradmin/external-dns-poweradmin-webhook/commit/9cd280f872c4f029f61e650fb3df90a45829fddc))
* guard zone cache with a mutex, evict removed zones, delete before create ([04a041f](https://github.com/poweradmin/external-dns-poweradmin-webhook/commit/04a041f829fff6bde441c33886ca57689b5fad3c))
* hide disabled records from external-dns and re-enable them instead of duplicating ([d906f55](https://github.com/poweradmin/external-dns-poweradmin-webhook/commit/d906f55ab850e9cb279969db6490f3132b10c819))
* raise default write timeout so a slow multi-zone records sweep is not cut off ([9918050](https://github.com/poweradmin/external-dns-poweradmin-webhook/commit/99180506816a05f2d575c2d04a2cde16e0fb9239))
* refuse HTTP redirects so writes are not silently converted to GETs ([65506eb](https://github.com/poweradmin/external-dns-poweradmin-webhook/commit/65506ebb132f8c68dc7d11ee5534c3e9be993b8c))
* require label boundary and canonical names when matching zones ([8892743](https://github.com/poweradmin/external-dns-poweradmin-webhook/commit/88927436781ba60ba2ecb4958ad58b7836ad4b89))
* send the full DNS name in record updates so apex updates are not renamed to @.&lt;zone&gt; ([d08feb0](https://github.com/poweradmin/external-dns-poweradmin-webhook/commit/d08feb05c5c69b4d636ed2062df76a6e761594d3))
* skip records shadowed by a more specific zone in Records ([ae973e6](https://github.com/poweradmin/external-dns-poweradmin-webhook/commit/ae973e638210be46120f0899cc2f4c04a3332dca))
* store SRV priority separately, reject malformed MX/SRV targets, trim only one TXT quote pair ([a7aaf7b](https://github.com/poweradmin/external-dns-poweradmin-webhook/commit/a7aaf7b1e8279c51cf29d4690ebf44721d083ef4))
* validate regex domain filter cleanly and check PowerAdmin reachability in readyz ([f8bccf9](https://github.com/poweradmin/external-dns-poweradmin-webhook/commit/f8bccf98ad87537b6b13d41f36fbc199b8d8d32a))


### Miscellaneous Chores

* release 1.4.0 ([af800ad](https://github.com/poweradmin/external-dns-poweradmin-webhook/commit/af800adfd7e608c386efaeba261014e2c767baa8))

## [1.3.0](https://github.com/poweradmin/external-dns-poweradmin-webhook/compare/v1.2.8...v1.3.0) (2026-05-30)


### Features

* support LUA record type for GSLB ([30edd55](https://github.com/poweradmin/external-dns-poweradmin-webhook/commit/30edd552402878f059f68fb5526046917324d478))
* support LUA record type for GSLB ([df6bbaf](https://github.com/poweradmin/external-dns-poweradmin-webhook/commit/df6bbafb00474cc17ccf30ec12417ba3b139e69f))

## [1.2.8](https://github.com/poweradmin/external-dns-poweradmin-webhook/compare/v1.2.7...v1.2.8) (2026-05-30)


### Bug Fixes

* idempotent record delete on 404 and typed API errors ([113616f](https://github.com/poweradmin/external-dns-poweradmin-webhook/commit/113616f1b55b66c3d56f8989c2229a9b915357f1))
* treat 404 on record delete as idempotent and surface typed API errors ([920b88a](https://github.com/poweradmin/external-dns-poweradmin-webhook/commit/920b88ae3752df0d2925e2cb9c831c09aff2291e))

## [1.2.7](https://github.com/poweradmin/external-dns-poweradmin-webhook/compare/v1.2.6...v1.2.7) (2026-05-24)


### Bug Fixes

* bump golang.org/x/net to v0.55.0 to address GO-2026-5026 ([a91ae01](https://github.com/poweradmin/external-dns-poweradmin-webhook/commit/a91ae016dc89b06f5b3828ec8c50cbed204c212d))
* bump golang.org/x/net to v0.55.0 to address GO-2026-5026 ([51b0e90](https://github.com/poweradmin/external-dns-poweradmin-webhook/commit/51b0e906b64fb1522c398319b120db71845be6a4))

## [1.2.6](https://github.com/poweradmin/external-dns-poweradmin-webhook/compare/v1.2.5...v1.2.6) (2026-05-10)


### Bug Fixes

* address GO-2026-4918 HTTP/2 CONTINUATION infinite loop ([8a73d16](https://github.com/poweradmin/external-dns-poweradmin-webhook/commit/8a73d16361d15e2e0b6a672e3816a6a900557323))
* bump golang.org/x/net to v0.54.0 and go to 1.26.3 for GO-2026-4918 ([30b4ed1](https://github.com/poweradmin/external-dns-poweradmin-webhook/commit/30b4ed13a075f34c446306e3aae5e431d7158da6))

## [1.2.5](https://github.com/poweradmin/external-dns-poweradmin-webhook/compare/v1.2.4...v1.2.5) (2026-04-28)


### Bug Fixes

* drop deprecated cosign --output-certificate from signs config ([13b61c0](https://github.com/poweradmin/external-dns-poweradmin-webhook/commit/13b61c0fa5c298521838fd68c992821dec8eb04d))
* drop deprecated cosign --output-certificate from signs config ([800319c](https://github.com/poweradmin/external-dns-poweradmin-webhook/commit/800319cf3d075ccf1a27a3be709d4efba2be7a8d))

## [1.2.4](https://github.com/poweradmin/external-dns-poweradmin-webhook/compare/v1.2.3...v1.2.4) (2026-04-27)


### Bug Fixes

* pin GitHub-owned actions by commit SHA ([9ba028e](https://github.com/poweradmin/external-dns-poweradmin-webhook/commit/9ba028e884b0fda6e1581a89d27cc6beda8c22e4))
* pin GitHub-owned actions by commit SHA ([4e6e418](https://github.com/poweradmin/external-dns-poweradmin-webhook/commit/4e6e41806641bf51e5cf51328445c15c60eab51c))

## [1.2.3](https://github.com/poweradmin/external-dns-poweradmin-webhook/compare/v1.2.2...v1.2.3) (2026-04-13)


### Bug Fixes

* bump golangci-lint to v2.11.4 for Go 1.26 compatibility ([413aa38](https://github.com/poweradmin/external-dns-poweradmin-webhook/commit/413aa38182db9faba95d834b420f794c7bed5e4e))

## [1.2.2](https://github.com/poweradmin/external-dns-poweradmin-webhook/compare/v1.2.1...v1.2.2) (2026-03-25)


### Bug Fixes

* use TARGETPLATFORM arg for binary path in GoReleaser Dockerfile ([3f0b5cd](https://github.com/poweradmin/external-dns-poweradmin-webhook/commit/3f0b5cdd365e8bf99b23ce21c902d52876db1d7e))

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
