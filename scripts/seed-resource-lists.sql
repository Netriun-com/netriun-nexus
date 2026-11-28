-- SPDX-License-Identifier: AGPL-3.0-only
-- Run only on a disposable, migrated ccmp_test database, with an Alibaba account:
-- psql "$TEST_DATABASE_URL" -v ON_ERROR_STOP=1 \
--   -c "SELECT set_config('nexus.seed_workspace','WORKSPACE_ID',false), set_config('nexus.seed_account','ACCOUNT_ID',false)" \
--   -f scripts/seed-resource-lists.sql
-- Seeds exactly 100,000 resources: 20,000 per existing inventory list kind.
-- Re-running replaces this script's fixtures. No provider credentials are used.
DO $$ BEGIN
 IF current_database()<>'ccmp_test' THEN RAISE EXCEPTION 'Use isolated ccmp_test'; END IF;
 IF NOT EXISTS(SELECT 1 FROM cloud_accounts WHERE workspace_id=current_setting('nexus.seed_workspace')::bigint AND id=current_setting('nexus.seed_account')::bigint AND provider='alibaba') THEN RAISE EXCEPTION 'Set an existing Alibaba workspace/account'; END IF;
END $$;
INSERT INTO resources(workspace_id,account_id,provider,service,kind,region,provider_id,name,state,tags,summary,details)
SELECT current_setting('nexus.seed_workspace')::bigint,current_setting('nexus.seed_account')::bigint,'alibaba',spec.service,spec.kind,
 CASE WHEN spec.kind='directory_user' THEN 'cn-shanghai' ELSE 'cn-hangzhou' END,
 't23-'||spec.kind||'-'||lpad(n::text,6,'0'),'Resource '||lpad(n::text,6,'0'),'running','{}',
 jsonb_build_object('instance_type','ecs.g7.large','public_ip','203.0.113.10','private_ip','10.0.0.1'),
 CASE spec.kind
 WHEN 'instance' THEN '{"tags":{},"vpc_id":"vpc-test","security_group_ids":[]}'::jsonb
 WHEN 'desktop' THEN jsonb_build_object('DesktopId','t23-desktop-'||lpad(n::text,6,'0'),'DesktopName','Desktop '||n,'DesktopStatus','Running','RegionId','cn-hangzhou','ChargeType','PostPaid')
 WHEN 'directory_user' THEN jsonb_build_object('EndUserId','t23-directory_user-'||lpad(n::text,6,'0'),'RealNickName','User '||n,'Status',0)
 WHEN 'bucket' THEN jsonb_build_object('name','t23-bucket-'||lpad(n::text,6,'0'),'region','cn-hangzhou','status','available','storage_bytes',1024,'object_count',2)
 ELSE jsonb_build_object('id','t23-security_group-'||lpad(n::text,6,'0'),'name','Security group '||n,'region','cn-hangzhou','rules_available',true,'rules','[]'::jsonb)
 END
FROM generate_series(1,20000) n CROSS JOIN (VALUES ('compute','instance'),('eds.desktops','desktop'),('eds.users','directory_user'),('oss.buckets','bucket'),('ecs.security_groups','security_group')) AS spec(service,kind)
ON CONFLICT(account_id,service,region,kind,provider_id) DO UPDATE SET name=excluded.name,state=excluded.state,tags=excluded.tags,summary=excluded.summary,details=excluded.details,seen_at=clock_timestamp();
INSERT INTO sync_state(account_id,service,region,"interval",last_success_at)
SELECT current_setting('nexus.seed_account')::bigint,service,region,interval '5 minutes',clock_timestamp()
FROM (VALUES ('compute','cn-hangzhou'),('eds.desktops','cn-hangzhou'),('eds.users','cn-shanghai'),('oss.buckets','*'),('ecs.security_groups','cn-hangzhou')) spec(service,region)
ON CONFLICT(account_id,service,region) DO UPDATE SET last_success_at=excluded.last_success_at,last_error='',last_error_code='';
UPDATE cloud_accounts SET last_sync_at=clock_timestamp() WHERE workspace_id=current_setting('nexus.seed_workspace')::bigint AND id=current_setting('nexus.seed_account')::bigint;
ANALYZE resources;
