-- +goose Up
-- +goose NO TRANSACTION

-- Rename existing values
ALTER TYPE attribute_value_type RENAME VALUE 'single' TO 'text';
ALTER TYPE attribute_value_type RENAME VALUE 'multi' TO 'multi_text';

-- Add new values
ALTER TYPE attribute_value_type ADD VALUE 'enum';
ALTER TYPE attribute_value_type ADD VALUE 'multi_enum';
ALTER TYPE attribute_value_type ADD VALUE 'boolean';

-- Store predefined choices for enum/multi_enum types
ALTER TABLE attribute_keys ADD COLUMN allowed_values jsonb;

-- +goose Down
ALTER TABLE attribute_keys DROP COLUMN IF EXISTS allowed_values;
-- Note: PostgreSQL does not support removing values from an enum or renaming them back easily.
-- A full down migration would require recreating the type, which is complex.
-- For safety, enum values are left as-is on rollback.
