-- Internal infrastructure management is the only supported deployment.
-- Historical schema and commercial records stay intact for migration compatibility.
UPDATE system_modes SET mode='self_use', locked=true;
ALTER TABLE system_modes ADD CONSTRAINT system_modes_internal_only CHECK (mode='self_use');

UPDATE system_settings SET deleted_at=now()
WHERE deleted_at IS NULL AND (key='platform.operation_mode' OR key LIKE 'registration.%' OR key LIKE 'billing.%');

UPDATE role_permissions SET deleted_at=now()
WHERE deleted_at IS NULL AND permission_id IN (
 SELECT id FROM permissions WHERE resource IN ('products','orders','billing','finance','marketing','operations')
);
UPDATE api_key_permissions SET deleted_at=now()
WHERE deleted_at IS NULL AND permission_id IN (
 SELECT id FROM permissions WHERE resource IN ('products','orders','billing','finance','marketing','operations')
);
UPDATE permissions SET deleted_at=now()
WHERE deleted_at IS NULL AND resource IN ('products','orders','billing','finance','marketing','operations');
