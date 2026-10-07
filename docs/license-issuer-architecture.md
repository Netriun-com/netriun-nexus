# Netriun offline license issuer architecture

Status: architecture and interfaces defined; proprietary issuer implementation
and production signing environment remain outside this public repository.

```text
Netriun operator
      |
      v
Offline issuer CLI --- private issuer database/audit log
      |
      v
Hardware signer (non-exportable Ed25519 key)
      |
      | signed JSON file
      v
Customer administrator ---> Nexus Core offline verifier
```

## Boundary

The issuer should live in a separate private repository and build pipeline. It
must not import Nexus `internal` packages, access a customer Nexus runtime, or
be required by Core at runtime. The published JSON schemas and JCS/Ed25519
test vectors are its interoperability contract. Issuer storage contains
commercial/customer issuance records and has no access to Core PostgreSQL.

## Required commands

The private CLI contract is:

- `license create --request ... --out ...`: validate approved customer,
  installation and entitlement input, obtain a hardware signature, verify the
  completed envelope, then write it atomically;
- `license inspect FILE`: print signed claims and fingerprints without
  modifying the file;
- `license verify FILE --keyring FILE`: run schema, JCS, signature, time and
  policy validation independently of Nexus;
- `license export LICENSE_ID --out FILE`: export the exact immutable signed
  artifact already recorded by the issuer;
- `key rotate`: run a new approved ceremony and prepare a public keyring change;
- `key retire KEY_ID`: stop new issuance while preserving old verification;
- `key revoke KEY_ID`: require an incident/change record and produce a Core
  keyring security update.

There is deliberately no command that silently changes signed claims. Changes
create a new license ID and audit event. M5 does not issue a customer license.

## Signing pipeline

1. Accept a structured issuance request, not arbitrary JSON.
2. validate product, edition, deployment mode, installation ID, approved
   features/limits, timestamps and customer identifiers;
3. construct only the v1 signed claims object;
4. canonicalize it with RFC 8785 JCS;
5. send the canonical digest/message to the hardware signer selected by
   `key_id`;
6. assemble the envelope and immediately verify it against the public key;
7. atomically persist the artifact, claims hash, operator/change-ticket IDs and
   public-key fingerprint;
8. export the exact recorded bytes through a controlled channel.

Unsigned metadata may describe delivery or an internal ticket, but Nexus must
never use it for product, edition, mode, customer, entitlement, limit, time,
binding or key decisions.

## Issuer security

- default-deny network posture; normal signing is offline;
- hardware-backed non-exportable key and dual-control operator authorization;
- append-only audit trail with artifact SHA-256, never private material;
- deterministic input and output validation;
- no secrets in command arguments, stdout, logs, crash dumps or telemetry;
- atomic file creation with restrictive permissions;
- separate development keys and databases that cannot use production key IDs;
- independent verification before export;
- backup and recovery exercises governed by the key ceremony procedure.

The public repository intentionally contains neither the proprietary issuer,
production key, customer records, nor a sample that can be mistaken for a
customer license.

