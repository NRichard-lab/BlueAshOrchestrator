-- Widen the sessions.harness CHECK to allow the two local harnesses:
--
--   local         Local (OpenCode): OpenCode 2 against one linked endpoint.
--   local-models  Local Models: the runner built into the ao binary.
--
-- 'local' shipped as a harness without a migration, so a session on it could
-- not be stored. It is allowed here together with the new 'local-models'.
--
-- SQLite cannot ALTER a CHECK constraint, so rewrite the sessions schema.
-- Every historical harness variant ends with the retained 'fake' fixture
-- harness, so anchoring on that tail widens whichever shape a database arrives
-- with. Each harness is added by its own statement, guarded by its own
-- absence, so a database that somehow already allows one still gains the
-- other. writable_schema changes run outside a transaction; RESET forces
-- SQLite to reparse the schema.
--
-- usage_bindings carries its own narrower harness CHECK (harnesses whose usage
-- AO can bind to a provider account). A local model has no provider account,
-- so it is left alone.

-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master
SET sql = replace(sql, '''fake''))', '''local'', ''fake''))')
WHERE type = 'table' AND name = 'sessions'
  AND sql LIKE '%CHECK (harness IN (%'
  AND sql LIKE '%''minimax-code''%'
  AND sql NOT LIKE '%''local''%';
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master
SET sql = replace(sql, '''fake''))', '''local-models'', ''fake''))')
WHERE type = 'table' AND name = 'sessions'
  AND sql LIKE '%CHECK (harness IN (%'
  AND sql LIKE '%''minimax-code''%'
  AND sql NOT LIKE '%''local-models''%';
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd

-- +goose Down
-- Harness repair may insert later harnesses after these instead of before
-- 'fake', so remove each entry wherever it sits in the list. ", 'local'" does
-- not match inside ", 'local-models'": the closing quote differs.
-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master
SET sql = replace(sql, ', ''local-models''', '')
WHERE type = 'table' AND name = 'sessions';
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master
SET sql = replace(sql, ', ''local''', '')
WHERE type = 'table' AND name = 'sessions';
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd
