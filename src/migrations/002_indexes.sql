-- +goose Up
CREATE INDEX idx_memory_user_active   ON memory(user_id, active);
CREATE INDEX idx_memory_user_slot     ON memory(user_id, slot, active);
CREATE INDEX idx_memory_user_entity   ON memory(user_id, slot, entity_key, active);
CREATE INDEX idx_memory_turn          ON memory(turn_id);
CREATE INDEX idx_memory_valid_until   ON memory(valid_until);

CREATE INDEX idx_turn_session         ON turn(session_id);
CREATE INDEX idx_turn_user            ON turn(user_id);

CREATE INDEX idx_entity_user_name     ON entity(user_id, name);
CREATE INDEX idx_entity_user_type     ON entity(user_id, type);

CREATE INDEX idx_edge_source          ON edge(source_id, relation);
CREATE INDEX idx_edge_target          ON edge(target_id, relation);
CREATE INDEX idx_edge_user_valid      ON edge(user_id, valid_until);

-- +goose Down
DROP INDEX IF EXISTS idx_edge_user_valid;
DROP INDEX IF EXISTS idx_edge_target;
DROP INDEX IF EXISTS idx_edge_source;
DROP INDEX IF EXISTS idx_entity_user_type;
DROP INDEX IF EXISTS idx_entity_user_name;
DROP INDEX IF EXISTS idx_turn_user;
DROP INDEX IF EXISTS idx_turn_session;
DROP INDEX IF EXISTS idx_memory_valid_until;
DROP INDEX IF EXISTS idx_memory_turn;
DROP INDEX IF EXISTS idx_memory_user_entity;
DROP INDEX IF EXISTS idx_memory_user_slot;
DROP INDEX IF EXISTS idx_memory_user_active;