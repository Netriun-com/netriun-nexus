# Cloud account permission guide

Netriun Nexus must use a dedicated application identity in every cloud. Never
connect an AWS root user, an Alibaba Cloud root account, a personal Azure
identity, or a human Google account. Start with read-only inventory access and
add mutation permissions only for operations that Nexus is allowed to perform.

The portal displays these requirements while a cloud connection is being
created. The JSON files in this directory remain the canonical copyable
policies for providers that support custom JSON policies.

## Amazon Web Services

Create a dedicated IAM user or role and apply
[aws-policy.json](aws-policy.json). The first statement grants the EC2 reads
used by inventory and live details. The second statement grants start, stop and
reboot; remove it for a read-only connection and replace `REGION` and
`ACCOUNT_ID` before attaching it.

Do not attach `AdministratorAccess`. If practical, replace the instance
wildcard with the exact instance ARNs Nexus may operate.

## Alibaba Cloud

Create a RAM user with a Permanent AccessKey and no console access. Apply
[alibaba-policy.json](alibaba-policy.json), then remove complete statements for
features the connection will not use:

- Remove ECS lifecycle and security-group mutation statements for read-only ECS.
- Remove `oss:PutBucket` when Nexus must only inventory OSS.
- Remove EDS lifecycle, user, command and billing statements when Nexus must
  only inventory WUYING resources.

Do not attach `AdministratorAccess`, `AliyunRAMFullAccess`, or product-wide
full-access policies. See [Alibaba Cloud connection](alibaba.md) for service and
region details.

## Microsoft Azure

Create a dedicated Microsoft Entra application and service principal. Assign
it at the narrowest subscription or resource-group scope:

- `Reader` for VM inventory and live details.
- `Virtual Machine Contributor` only when Nexus must start, deallocate or
  restart virtual machines.

Record the Tenant ID, Application (Client) ID, Client Secret and Subscription
ID. Client secrets expire and must be rotated before their expiration date.

## Google Cloud

Enable the Compute Engine API and create a dedicated service account in the
target project:

- `roles/compute.viewer` for inventory and live details.
- `roles/compute.instanceAdmin.v1` only when Nexus must start, stop or reset
  virtual machines.

Create a JSON key only when workload identity is not available, store it as a
secret, and rotate it regularly. See [Azure and Google Cloud
connections](azure-gcp.md) for the complete connection workflow.

## Future billing reports

Exact spend is intentionally unavailable until a provider billing connector is
configured. Cost APIs require separate permissions from resource inventory.
When those connectors are added, use a distinct read-only billing role instead
of expanding the infrastructure identity with broad financial access.
