-- 资产直接归属机房；不回填历史机柜关联。
ALTER TABLE assets ADD COLUMN datacenter_id bigint;
ALTER TABLE assets ADD CONSTRAINT fk_assets_datacenter
    FOREIGN KEY (tenant_id, project_id, datacenter_id)
    REFERENCES datacenters(tenant_id, project_id, id);
COMMENT ON COLUMN assets.datacenter_id IS '所属机房；空表示未分配';
CREATE INDEX ix_assets_datacenter_id ON assets (tenant_id, project_id, datacenter_id);

ALTER TABLE assets DROP COLUMN rack_id, DROP COLUMN rack_u_position, DROP COLUMN height_u;
DROP TABLE rack_labels;
DROP TABLE racks;

DELETE FROM role_permissions WHERE permission_id IN (SELECT id FROM permissions WHERE resource IN ('racks', 'rack_labels', 'rack-labels'));
DELETE FROM api_key_permissions WHERE permission_id IN (SELECT id FROM permissions WHERE resource IN ('racks', 'rack_labels', 'rack-labels'));
DELETE FROM permissions WHERE resource IN ('racks', 'rack_labels', 'rack-labels');
