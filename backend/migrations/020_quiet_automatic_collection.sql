-- Release completed internal polling jobs without deleting the per-device fencing counter.
ALTER TABLE device_leases ALTER COLUMN operation_id DROP NOT NULL;
