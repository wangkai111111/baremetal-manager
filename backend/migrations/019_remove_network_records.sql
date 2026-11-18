-- Remove unused manual ledgers. Runtime PXE uses networks, ip_pools and its persisted service config.
DROP TABLE pxe_configs;
DROP TABLE ip_allocations;
