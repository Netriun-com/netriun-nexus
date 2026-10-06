// SPDX-License-Identifier: AGPL-3.0-only

'use strict';

window.NEXUS_POLICY_GUIDES = {
  aws: {
    title: 'AWS IAM policy',
    identity: 'Create a dedicated IAM user or role. Do not use the root account.',
    permissions: ['EC2 inventory: DescribeRegions, DescribeInstances, DescribeSecurityGroups, DescribeVolumes, DescribeNetworkInterfaces', 'Optional lifecycle: StartInstances, StopInstances, RebootInstances on approved instance ARNs'],
    policy: `{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "ec2:DescribeRegions", "ec2:DescribeInstances",
        "ec2:DescribeSecurityGroups", "ec2:DescribeVolumes",
        "ec2:DescribeNetworkInterfaces"
      ],
      "Resource": "*"
    },
    {
      "Effect": "Allow",
      "Action": ["ec2:StartInstances", "ec2:StopInstances", "ec2:RebootInstances"],
      "Resource": "arn:aws:ec2:REGION:ACCOUNT_ID:instance/*"
    }
  ]
}`
  },
  alibaba: {
    title: 'Alibaba Cloud RAM policy',
    identity: 'Create a dedicated RAM user with a Permanent AccessKey and no console access.',
    permissions: ['ECS inventory, lifecycle, security groups and rules', 'WUYING EDS inventory, users, lifecycle, policies and commands', 'OSS bucket inventory, statistics and optional private-bucket creation', 'Read-only exact monthly billing rows through BSS OpenAPI'],
    policy: `{
  "Version": "1",
  "Statement": [
    {"Effect":"Allow","Action":["ecs:DescribeRegions","ecs:DescribeInstances","ecs:DescribeSecurityGroups","ecs:DescribeSecurityGroupAttribute"],"Resource":"*"},
    {"Effect":"Allow","Action":["ecs:AuthorizeSecurityGroup","ecs:AuthorizeSecurityGroupEgress","ecs:RevokeSecurityGroup","ecs:RevokeSecurityGroupEgress"],"Resource":"*"},
    {"Effect":"Allow","Action":["ecs:StartInstance","ecs:StopInstance","ecs:RebootInstance"],"Resource":"acs:ecs:*:*:instance/*"},
    {"Effect":"Allow","Action":["oss:ListBuckets","oss:GetBucketInfo","oss:GetBucketStat","oss:PutBucket"],"Resource":"acs:oss:*:*:*"},
    {"Effect":"Allow","Action":["ecd:DescribeDesktops","ecd:DescribeRegions","ecd:DescribeBundles","ecd:DescribeDesktopTypes","ecd:DescribeImages","ecd:DescribeInvocations","ecd:DescribeOfficeSites","ecd:DescribePolicyGroups","ecd:DescribeUsers"],"Resource":"*"},
    {"Effect":"Allow","Action":["ecd:StartDesktops","ecd:StopDesktops","ecd:RebootDesktops","ecd:ModifyUserEntitlement","ecd:CreateUsers","ecd:ModifyDesktopsPolicyGroup","ecd:SetDesktopMaintenance","ecd:RunCommand"],"Resource":"*"},
    {"Effect":"Allow","Action":["ecd:CreateDesktops","ecd:RenewDesktops","ecd:ModifyDesktopChargeType"],"Resource":"*"},
    {"Effect":"Allow","Action":["bssapi:DescribeInstanceBill"],"Resource":"*"}
  ]
}`
  },
  azure: {
    title: 'Microsoft Azure RBAC roles',
    identity: 'Create a dedicated Microsoft Entra application/service principal scoped to the target subscription or resource group.',
    permissions: ['Reader for inventory and live details', 'Virtual Machine Contributor only when Nexus must start, deallocate or restart VMs'],
    policy: ''
  },
  gcp: {
    title: 'Google Cloud IAM roles',
    identity: 'Create a dedicated service account in the target project and enable the Compute Engine API.',
    permissions: ['roles/compute.viewer for inventory and live details', 'roles/compute.instanceAdmin.v1 only when Nexus must start, stop or reset VMs'],
    policy: ''
  }
};

// Documentation is data-driven so this information architecture can grow
// without coupling article content to portal routing or UI behavior.
window.NEXUS_DOCS = [
  {id:'getting-started',category:'Getting Started',title:'Start with Netriun Nexus',summary:'Create a verified workspace, add your team, and connect the first cloud.',steps:['Register with a unique email and verify it from the message sent by Nexus.','Sign in and open Cloud connections.','Connect a dedicated cloud identity and test it before saving.','Open a compute service to see the latest snapshot immediately while Nexus refreshes it in the background.'],notes:['Use a dedicated cloud identity. Never connect a root or personal administrator credential.']},
  {id:'team-members',category:'Getting Started',title:'Invite your team',summary:'A Community workspace includes its owner plus five members.',steps:['Open Team members and add a unique username and email.','The member verifies their email before signing in.','Create an access policy and add the member with a viewer, operator, or manager role.']},
  {id:'cloud-connect',category:'Cloud Connections',title:'Connect a cloud account',summary:'The connection wizard validates identity and compute access before saving.',steps:['Choose AWS, Alibaba Cloud, Microsoft Azure, or Google Cloud.','Enter a friendly name, optional owner, access policy, and optional region filters.','Enter the provider-specific credential.','Run Test connection, then save within ten minutes.'],notes:['Credentials are encrypted at rest and are never returned by the API.','A successful test is bound to the signed-in user, provider, and exact credential values, and is consumed when saved.']},
  {id:'cloud-aws',provider:'aws',category:'Cloud Connections',title:'Amazon Web Services',summary:'Connect a dedicated IAM access key for EC2 inventory and lifecycle operations.',steps:['Create an IAM principal for Nexus.','Apply the policy shown below after replacing REGION and ACCOUNT_ID.','Remove the lifecycle statement for an inventory-only connection.','Enter the AccessKey ID, secret, and optional temporary session token.'],notes:['Never connect the AWS root user or attach AdministratorAccess.']},
  {id:'cloud-alibaba',provider:'alibaba',category:'Cloud Connections',title:'Alibaba Cloud',summary:'Connect a RAM user for ECS, WUYING EDS, OSS, and exact billing reports.',steps:['Create a dedicated RAM user and Permanent AccessKey without console access.','Apply only the ECS, EDS, OSS, and read-only BSS billing statements required by the enabled features.','Remove mutation statements for a read-only connection.','Enter the AccessKey ID and secret, test, then save.'],notes:['Do not attach AdministratorAccess, AliyunRAMFullAccess, or product-wide full-access policies.','Exact billing reports require bssapi:DescribeInstanceBill with Resource *.']},
  {id:'cloud-azure',provider:'azure',category:'Cloud Connections',title:'Microsoft Azure',summary:'Use a Microsoft Entra service principal scoped to a subscription.',steps:['Register an Entra application and create its service principal.','Assign the minimum role shown below at subscription or resource-group scope.','Copy Tenant ID, Application Client ID, Client Secret, and Subscription ID.','Enter those values in the connection wizard and run the test.'],notes:['Client secrets expire. Record the expiration and rotate before it is reached.']},
  {id:'cloud-gcp',provider:'gcp',category:'Cloud Connections',title:'Google Cloud',summary:'Use a dedicated service-account JSON key scoped to one project.',steps:['Enable the Compute Engine API in the project.','Assign the minimum role shown below to a dedicated service account.','Create and download a JSON key.','Enter the Project ID, paste the complete JSON key, and run the test.'],notes:['Service-account keys are long-lived secrets. Rotate and revoke unused keys.']},
  {id:'compute-overview',category:'Services',title:'Compute inventory',summary:'Fetch, search, filter, inspect, and operate compute instances across connected clouds.',steps:['Choose one cloud provider and select one or more accounts from that provider.','Open its compute service to display the latest snapshot and queue a background refresh.','Use list or visual mode, search, filters, sorting, and pagination without triggering another provider request.','Choose Refresh live when you need to explicitly queue another refresh.','Open a resource for provider-native live details.']},
  {id:'ecs-security-groups',category:'Services',title:'Alibaba ECS security groups',summary:'Inspect instance bindings and manage inbound and outbound network rules.',steps:['Select exactly one Alibaba account and open Security groups.','Use list or visual mode to search, filter, and sort the saved snapshot.','Open Rules to inspect protocol, ports, source or destination, policy, and priority.','Account Managers can add a validated rule or delete a rule by its provider rule ID.','Open an ECS instance to see its attached security groups.'],notes:['Prefer a narrow trusted CIDR instead of 0.0.0.0/0 or ::/0.','Remove the Authorize/Revoke actions from the RAM policy for read-only connections.','Service-managed security groups are displayed but cannot be changed from Nexus.']},
  {id:'eds-desktops',category:'Services',title:'Alibaba EDS desktops',summary:'Manage WUYING desktops across available regions.',steps:['Select an Alibaba Cloud connection.','Choose a region or All available regions.','Create, renew, start, stop, and manage desktops.','Use remote command only on a running desktop and review its returned output.']},
  {id:'eds-users',category:'Services',title:'Alibaba EDS users',summary:'Create convenience users and manage desktop assignments across regions.',steps:['Select an Alibaba account and EDS Users.','Create the convenience user.','Open bound desktops for that user.','Bind or unbind desktops across available regions.']},
  {id:'oss-buckets',category:'Services',title:'Alibaba OSS buckets',summary:'Inventory object storage usage and create private buckets.',steps:['Select exactly one Alibaba account and open OSS buckets.','Use list or visual mode to inspect region, storage class, redundancy, ACL, versioning, used storage, and object count.','Choose Refresh live to update the database snapshot without blocking page navigation.','Choose Create bucket, select its region, storage class, and redundancy, then confirm the cost impact.'],notes:['Buckets created by Nexus use a private ACL.','Alibaba OSS usage statistics may be delayed by more than one hour.','Remove oss:PutBucket from the RAM policy when the connection should remain read-only.']},
  {id:'access-groups',category:'Access Management',title:'Policies and account roles',summary:'Give each person or team the right role for each cloud account.',steps:['Open Access policies and review the effective user-by-account matrix.','Create teams and manage their viewer, operator, or manager memberships.','Choose Assign access and select a person or team, cloud account, and account role.','Use Viewer for read-only inventory, Operator for lifecycle actions, or Account Manager for connection administration.','Review the matrix again to confirm the effective result.'],notes:['The first explicit assignment safely converts effective legacy access for that account before the new policy becomes authoritative.','Workspace administrators retain access to every account in their workspace.']},
  {id:'single-sign-on',category:'Access Management',title:'OIDC and SAML single sign-on',summary:'Federate workspace identity while keeping cloud authorization inside Nexus.',steps:['Open Workspace settings and add an OIDC or SAML identity provider.','For OIDC, configure the exact callback URL; for SAML, import the generated SP metadata into the IdP.','Enable the provider and copy its direct sign-in URL or distribute the workspace SSO code.','Map exact IdP group values to Nexus teams and choose the membership role cap.','Confirm sign-in and effective cloud-account access before requiring SSO.'],notes:['The Workspace Owner always retains local break-glass login.','OIDC requires a verified email claim; SAML requires a persistent NameID.','SSO group synchronization never removes manual team memberships.']},
  {id:'audit-log',category:'Access Management',title:'Audit log',summary:'Review workspace changes and cloud-operation intent.',steps:['Open Audit log as a workspace administrator.','Search by action, username, or target.','Investigate failed and accepted cloud operations using their target resource.']},
  {id:'lifecycle',category:'Operations',title:'Start, stop, and reboot',summary:'Lifecycle actions use the permissions of the connected cloud identity.',steps:['Confirm the selected account and resource.','Submit the action from list, visual, or detail view.','Wait for the next collection to confirm the provider-observed state.'],notes:['Stopping resources can interrupt users and workloads.']},
  {id:'workspace-settings',category:'Operations',title:'Live inventory and retention',summary:'Compute pages remain responsive by showing the latest healthy snapshot while live collection runs in the background.',steps:['Open a compute service page to queue an asynchronous provider refresh.','Watch the refresh status above the inventory while continuing to use the page.','Use Refresh live to explicitly queue or join the current refresh.','Open Workspace settings to configure audit retention.'],notes:['A failed provider request does not erase the last successful snapshot.','Equivalent in-flight requests are coalesced and account/region concurrency is limited to protect provider APIs.']},
  {id:'reports',category:'Operations',title:'Resource and billing reports',summary:'Review resource consumption and build exact Alibaba monthly cost reports.',steps:['Choose Alibaba Cloud and one or more accounts in the sidebar.','Open Reports, choose a month and select ECS, EDS, or both.','Optionally filter exact bill rows by resource name or instance ID, then choose Sync exact bill.','Review payable pre-tax totals per service and currency.','Export the filtered report to Excel or PDF.'],notes:['The RAM user needs bssapi:DescribeInstanceBill; Nexus does not estimate missing spend.','Alibaba billing data can lag by about 24 hours, resource metadata can lag by about 48 hours, and the current month is provisional until after the third day of the next month.','The connector supports the latest 18 billing months.']},
  {id:'connection-errors',category:'Troubleshooting',title:'Connection test failed',summary:'Check identity values, credential expiry, API enablement, and role scope.',steps:['Confirm every ID belongs to the same account, tenant, subscription, or project.','Check that the credential is active and not expired.','Verify compute API access and the minimum role.','Remove region filters temporarily and test again.']},
  {id:'sync-errors',category:'Troubleshooting',title:'Live refresh failed',summary:'A failed provider read preserves the last successful database snapshot.',steps:['Open the connection and run Test connection with a fresh credential if needed.','Verify provider API availability and permissions.','Return to the service page and choose Refresh live.','Use Audit log to identify the affected connection.']},
  {id:'email-errors',category:'Troubleshooting',title:'Verification email problems',summary:'Recover an unverified portal account without creating another workspace.',steps:['Check spam and mailbox filtering.','Return to Sign in and enter the registered email.','After the verification warning, choose Resend verification email.','Use only the newest link; verification links expire after 24 hours.']},
  {id:'api-reference',category:'Troubleshooting',title:'API reference',summary:'Use the versioned REST API for automation and diagnostics.',steps:['Open API reference in the portal.','Authenticate with a verified email to receive a 12-hour bearer token.','Follow the same access-policy boundaries used by the portal.','Use docs/openapi.json as the machine-readable contract.']}
];
