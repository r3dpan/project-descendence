-- +goose Up

-- Task 6.6. mount-type param values (task 6.1's contract, §4.6's Podman
-- secrets) never belong in runs.params_json: that column is what every API
-- response returns from (task 6.5's redaction notwithstanding, defence in
-- depth beats relying on it alone), and an audit trail that can leak a
-- secret by way of a future response-shaping bug is not one. A dedicated
-- column, split out at resolution (manifest.ResolveParams) rather than
-- redacted after the fact, is what makes "params_json never held this to
-- begin with" true rather than "params_json held it and something else
-- remembered to hide it".
--
-- Same shape as params_json: an array of {"name":..., "value":...} in
-- contract order, holding only the mount-type entries. Selected by every
-- run query alongside params_json (see runs.sql) - the safety boundary
-- against an API response leaking it is the Go response structs, which
-- have no field for it, not which SQL columns a query happens to select.
ALTER TABLE runs ADD COLUMN secret_params_json jsonb NOT NULL DEFAULT '[]';

-- +goose Down

ALTER TABLE runs DROP COLUMN secret_params_json;
