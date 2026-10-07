# Production signing-key ceremony

Status: procedure ready; the first production ceremony has **not** been run.
The current development workstation and Kubernetes cluster are not approved
custody environments for a production signing key.

## Approved target environment

The recommended target is a dedicated offline issuer workstation connected to
a hardware cryptographic module that natively supports Ed25519. The private key
must be generated inside the module as non-exportable. Access should require
two authorized Netriun custodians, with separate administrator/operator
credentials. The workstation must have no CI agent, container runtime,
Kubernetes access, general web browsing, or customer application data.

An encrypted file key is not the production recommendation. It may be used only
for development fixtures, never with the production `key_id` or trust root.
Selection of the exact HSM and physical custody locations requires owner
approval before the ceremony.

## Immutable public metadata

Each public keyring entry contains:

- an immutable `key_id`, recommended format
  `nexus-license-prod-YYYY-NN`;
- `algorithm: Ed25519`;
- base64url, unpadded public key bytes;
- `state`: `active`, `retired`, or `revoked`;
- canonical UTC `created_at` with whole seconds;
- `usage: license_signing`.

Changing the public key requires a new `key_id`; a `key_id` must never be
reused. `retired` keys continue verifying old licenses. `revoked` keys fail
immediately after the updated keyring ships in Core.

## Ceremony roles and prerequisites

At least two people participate: a ceremony operator and an independent
witness. Before generation they record the clean workstation image checksum,
HSM model/serial and firmware, date/time source, room/location, attendees,
approved key ID, and the source revision of the public schema. Recording must
contain no PIN, seed, private-key bytes, recovery secret, or customer data.

## Ceremony sequence

1. Isolate and inspect the workstation; verify its signed operating-system and
   issuer-tool artifacts from two independent media/readers.
2. Initialize or unlock the HSM under dual control. Confirm Ed25519 generation
   and signing happen inside the module and that private export is disabled.
3. Generate one signing key using the pre-approved immutable `key_id`.
4. Export only the raw public key and metadata. Compute and independently
   compare its SHA-256 fingerprint on two systems.
5. Sign a non-customer ceremony challenge and verify it with the exported
   public key using the Core verifier/test vector.
6. Create the vendor-supported protected backup under dual control, then prove
   recovery into a spare isolated module. Do not expose private material during
   the recovery test.
7. Place the primary and backup modules/media in separate controlled physical
   locations. Record custodians and tamper-evident seal identifiers.
8. Commit only the reviewed public keyring entry and sanitized ceremony record.
9. Run keyring, signature, rotation, revocation, CodeQL, image and deployment
   tests before promoting a Core release.

## Backup, recovery, and compromise

There must be at least one tested vendor-supported encrypted backup stored
separately from the primary module. Recovery requires dual control and an audit
record. A lost primary with an intact protected backup is recovered under the
same `key_id`; a suspected compromise is not recovered for signing—it is marked
`revoked`, a new key ID is generated, and affected licenses are reviewed for
reissue. Normal rotation marks the old key `retired`, not `revoked`.

The operational record must define cryptoperiod, access review, failed-access
alerts, inventory checks, recovery-test frequency, compromise escalation, and
secure destruction. This follows the lifecycle and backup principles in NIST
SP 800-57 but still requires a Netriun-specific operating policy and security
review.

## Public artifacts

Safe repository artifacts are the public keyring entry, its fingerprint, a
sanitized ceremony record, and public verification vectors. Private-key
material, HSM credentials, recovery shares, internal audit logs, and the issuer
database remain outside this repository, images, CI, chat, and Kubernetes.

