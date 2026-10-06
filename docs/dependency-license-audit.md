# Dependency license audit

Audit date: 2026-10-06
Scope: modules linked by `go list -deps ./cmd/...`, repository assets, and
declared build/runtime container images. This is an engineering compatibility
review, not legal advice.

## Result

The two shipped commands currently link **55 external Go modules**:

| Detected license family | Module count | AGPL-3.0-only compatibility result |
| --- | ---: | --- |
| Apache-2.0 | 30 | No incompatibility identified; preserve license and NOTICE material. |
| BSD-2-Clause | 4 | No incompatibility identified; preserve copyright/license text. |
| BSD-3-Clause | 10 | No incompatibility identified; preserve copyright/license text. |
| MIT-style | 11 | No incompatibility identified; preserve copyright/license text. |

No linked Go module was detected under GPL-2.0-only, a source-available
license, or a proprietary license. No dependency was removed or replaced.
`NOTICE` now preserves the NOTICE material found in AWS SDK for Go, Smithy Go,
and CoreOS OIDC. The image build also runs
`scripts/collect-third-party-licenses.sh`, which fails when a linked module has
no root license text and copies the exact per-version license/NOTICE files to
`/usr/share/licenses/netriun-nexus`. Release artifacts outside the image must
include the same bundle; a summary NOTICE does not replace each dependency's
license conditions.

## Linked Go module inventory

### Apache-2.0

- `github.com/alibabacloud-go/alibabacloud-gateway-spi v0.0.5`
- `github.com/alibabacloud-go/bssopenapi-20171214/v6 v6.1.1`
- `github.com/alibabacloud-go/darabonba-openapi/v2 v2.2.4`
- `github.com/alibabacloud-go/debug v1.0.1`
- `github.com/alibabacloud-go/ecd-20200930/v5 v5.26.5`
- `github.com/alibabacloud-go/ecs-20140526/v7 v7.11.1`
- `github.com/alibabacloud-go/eds-user-20210308/v2 v2.2.2`
- `github.com/alibabacloud-go/tea v1.5.2`
- `github.com/alibabacloud-go/tea-utils/v2 v2.0.9`
- `github.com/aliyun/alibabacloud-oss-go-sdk-v2 v1.5.3`
- `github.com/aliyun/credentials-go v1.4.5`
- `github.com/aws/aws-sdk-go-v2 v1.46.0`
- `github.com/aws/aws-sdk-go-v2/credentials v1.20.3`
- `github.com/aws/aws-sdk-go-v2/internal/configsources v1.5.2`
- `github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.8.2`
- `github.com/aws/aws-sdk-go-v2/service/ec2 v1.329.0`
- `github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.19`
- `github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.14.2`
- `github.com/aws/smithy-go v1.28.1`
- `github.com/coreos/go-oidc/v3 v3.21.0`
- `github.com/go-jose/go-jose/v4 v4.1.4`
- `github.com/jonboulle/clockwork v0.5.0`
- `github.com/mattermost/xml-roundtrip-validator v0.1.0`
- `github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd`
- `github.com/modern-go/reflect2 v1.0.2`
- `github.com/richardlehane/mscfb v1.0.7`
- `github.com/richardlehane/msoleps v1.0.6`
- `github.com/russellhaering/goxmldsig v1.6.0`
- `github.com/tjfoc/gmsm v1.4.1`
- `gopkg.in/ini.v1 v1.67.0`

### BSD-2-Clause

- `github.com/beevik/etree v1.6.0`
- `github.com/crewjam/saml v0.5.1`
- `github.com/gorilla/websocket v1.5.3`
- `github.com/redis/go-redis/v9 v9.22.0`

### BSD-3-Clause

- `github.com/xuri/efp v0.0.1`
- `github.com/xuri/excelize/v2 v2.11.0`
- `github.com/xuri/nfp v0.0.2-0.20250530014748-2ddeb826f9a9`
- `golang.org/x/crypto v0.56.0`
- `golang.org/x/net v0.57.0`
- `golang.org/x/oauth2 v0.37.0`
- `golang.org/x/sync v0.22.0`
- `golang.org/x/sys v0.47.0`
- `golang.org/x/text v0.41.0`
- `golang.org/x/time v0.4.0`

### MIT-style

- `github.com/cespare/xxhash/v2 v2.3.0`
- `github.com/clbanning/mxj/v2 v2.7.0`
- `github.com/go-pdf/fpdf v0.9.0`
- `github.com/golang-jwt/jwt/v4 v4.5.2`
- `github.com/jackc/pgpassfile v1.0.0`
- `github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761`
- `github.com/jackc/pgx/v5 v5.10.0`
- `github.com/jackc/puddle/v2 v2.2.2`
- `github.com/json-iterator/go v1.1.12`
- `github.com/tiendc/go-deepcopy v1.7.2`
- `go.uber.org/atomic v1.11.0`

## Container and tool findings

| Component | Repository reference | Finding |
| --- | --- | --- |
| Nexus runtime | pinned distroless Debian 12 digest | Base project is Apache-2.0; Debian package notices remain component-specific. Release SBOM/license bundle still required. |
| Go builder | `golang:1.26-alpine` | Build-only, floating tag. Pin digest and capture Alpine package licenses for reproducible release builds. |
| PostgreSQL | `postgres:17-alpine` | PostgreSQL License is permissive/compatible, but the tag is floating and Alpine components require their own notices. |
| Redis server | `redis:7-alpine` | **Decision blocker.** The resolved local and cluster image is Redis 7.4.11. Redis 7.4.x is offered under RSALv2 or SSPLv1, neither an OSI-approved Open Source license. It is a separate service rather than linked Go code, but shipping this default conflicts with the stated “all Community components Open Source” objective and needs an explicit product/legal choice. |
| go-redis client | `github.com/redis/go-redis/v9 v9.22.0` | BSD-2-Clause and compatible; this is distinct from the Redis server license. |
| Trivy | pinned `ghcr.io/aquasecurity/trivy:0.75.0` digest | Apache-2.0 tooling used for scanning, not linked into Nexus. |

No Redis reference was changed in M3 because the instruction was to report
license concerns before removal or replacement. Viable decisions include
pinning Redis 7.2.x after reviewing its security/support implications, moving
to Redis 8 and deliberately selecting its AGPLv3 option, or validating a
compatible alternative through application tests. This document does not pick
one.

## First-party and copied/generated code review

- Git history identifies one current author identity and no third-party
  copyright headers in tracked first-party source.
- No `Code generated` marker, copied-source marker, vendored directory, or
  checked-in generated Go source was found.
- `docs/openapi.json` is treated as the AGPL API contract through the root
  path-level notice because JSON cannot contain an SPDX comment.
- `internal/app/web/logo.svg` is treated as a brand asset outside the software
  grant; a separate brand usage policy remains undecided.
- Module source is consumed through Go modules and is not vendored. Its
  upstream license obligations still apply to linked/distributed binaries.

## Release controls still required

Before Community beta distribution, automate all of the following:

1. build-dependency license classification from the exact module graph;
2. publish the generated third-party license/attribution bundle beside any
   downloadable artifacts (it is already embedded in the image);
3. SPDX or CycloneDX SBOM generation for the exact signed image;
4. policy failure on unknown, missing, or disallowed licenses; and
5. digest pinning and license review for every runtime/build image.

The audit must be rerun whenever `go.mod`, `go.sum`, a base image digest, or a
runtime image reference changes.
