CREATE TABLE IF NOT EXISTS alibaba_billing_items (
 id bigserial PRIMARY KEY,
 workspace_id bigint NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
 account_id bigint NOT NULL,
 billing_cycle text NOT NULL CHECK(billing_cycle ~ '^[0-9]{4}-(0[1-9]|1[0-2])$'),
 service_key text NOT NULL CHECK(service_key IN ('ecs','eds','other')),
 product_code text NOT NULL DEFAULT '',
 product_name text NOT NULL DEFAULT '',
 product_detail text NOT NULL DEFAULT '',
 instance_id text NOT NULL DEFAULT '',
 instance_name text NOT NULL DEFAULT '',
 region text NOT NULL DEFAULT '',
 subscription_type text NOT NULL DEFAULT '',
 currency text NOT NULL DEFAULT '',
 pretax_gross_amount numeric(24,8) NOT NULL DEFAULT 0,
 invoice_discount numeric(24,8) NOT NULL DEFAULT 0,
 pretax_amount numeric(24,8) NOT NULL DEFAULT 0,
 cash_amount numeric(24,8) NOT NULL DEFAULT 0,
 payment_amount numeric(24,8) NOT NULL DEFAULT 0,
 raw jsonb NOT NULL DEFAULT '{}',
 fetched_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY(account_id,workspace_id) REFERENCES cloud_accounts(id,workspace_id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS alibaba_billing_items_report_idx
 ON alibaba_billing_items(workspace_id,account_id,billing_cycle,service_key);
CREATE INDEX IF NOT EXISTS alibaba_billing_items_resource_idx
 ON alibaba_billing_items(workspace_id,lower(instance_name),lower(instance_id));

UPDATE access_roles
 SET permissions=(SELECT ARRAY(SELECT DISTINCT p FROM unnest(permissions || ARRAY['billing.view']) p ORDER BY p))
 WHERE built_in AND key IN ('viewer','operator','account_manager');
UPDATE access_roles
 SET permissions=(SELECT ARRAY(SELECT DISTINCT p FROM unnest(permissions || ARRAY['billing.sync']) p ORDER BY p))
 WHERE built_in AND key='account_manager';

CREATE OR REPLACE FUNCTION seed_nexus_access_roles() RETURNS trigger AS $$
BEGIN
 INSERT INTO access_roles(workspace_id,key,name,description,permissions,built_in) VALUES
  (NEW.id,'viewer','Viewer','Read cloud inventory, service details, and stored billing reports',ARRAY['account.view','compute.view','eds.view','billing.view'],true),
  (NEW.id,'operator','Operator','Viewer access plus compute and desktop lifecycle operations',ARRAY['account.view','compute.view','compute.operate','eds.view','eds.operate','billing.view'],true),
  (NEW.id,'account_manager','Account Manager','Operate resources, manage the cloud connection, and synchronize billing',ARRAY['account.view','account.manage','compute.view','compute.operate','eds.view','eds.operate','eds.manage','billing.view','billing.sync'],true)
 ON CONFLICT(workspace_id,key) DO NOTHING;
 RETURN NEW;
END;
$$ LANGUAGE plpgsql;

COMMENT ON TABLE alibaba_billing_items IS 'Exact Alibaba BSS instance-bill rows, atomically replaced per account and billing cycle.';
