# Installation identity, cloning, and disaster recovery

The PostgreSQL `installation_identity` row is authoritative. The offline
license is bound to its `installation_id`; there is no hostname, node, VM or
namespace bypass.

| Scenario | Identity classification | License result | Required action |
| --- | --- | --- | --- |
| Normal process/Pod restart | Same installation | Remains valid | None |
| Node replacement with same PostgreSQL data | Same installation | Remains valid | Restore storage and verify ID |
| Authoritative PostgreSQL backup/restore replacing the failed system | Same installation | Remains valid | Preserve the identity row and record DR event |
| Database restore into a fresh host as the sole recovery environment | Same installation | Remains valid | Ensure original is not active concurrently |
| Fresh database | New installation | Old license rejected | Request reissue |
| VM clone including PostgreSQL | Duplicate of the same identity, not a new licensed installation | Cryptographically verifies, but is not automatically licensed for concurrent use | Keep only one active or request transfer/additional license |
| Kubernetes namespace clone using the same external database | Same installation | Verifies | Must not run as an independent licensed environment |
| Namespace clone with copied database/PV | Duplicate identity | Verifies cryptographically | Isolate/disable clone; reissue if it becomes independent |
| Full environment clone | Duplicate identity | Verifies cryptographically | No bypass; commercial/operational control and reissue required |
| DR test restored in an isolated environment | Same identity | Verifies | Prevent production concurrency; document test window |
| Lost/corrupt identity row or rebuilt database | New installation | Old license rejected | Restore authoritative backup or request reissue |

The verifier rejects a license presented by a different installation ID. A
byte-for-byte database clone necessarily copies the same identity, so offline
cryptography alone cannot distinguish it from the original. Detection of
concurrent clones would require an online authority and is intentionally not
implemented. Operational controls, audit records and commercial terms must
cover that case.

Migration 10 stores a license time floor with the installation identity. Core
re-evaluates the license while running, prevents a backwards wall-clock jump
from reactivating Enterprise capabilities, and falls back to Community. An
administrator who rolls back both the database and system clock can also roll
back this local evidence; this is outside the offline verifier's trust boundary
and does not create a supported bypass.

