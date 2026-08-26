-- CISync dev Postgres bootstrap (runs on fresh volumes only).
-- Capacity headroom: service pools (64+32+20) + tests + admin must fit.
ALTER SYSTEM SET max_connections = '300';
ALTER ROLE cisync SET statement_timeout = '15s';
ALTER ROLE cisync SET lock_timeout = '8s';
ALTER ROLE cisync SET idle_in_transaction_session_timeout = '30s';
