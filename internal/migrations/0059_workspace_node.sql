-- A workspace can be pinned to a node. Purely additive.
--
-- workspaces.node_id: new apps created in the workspace are placed on this
-- node (an explicit choice by an account that manages nodes still wins; NULL
-- falls back to the panel default node, then the local host). Existing apps
-- stay where they are. Deleting the node clears the pin. The application
-- enforces placement when apps are created.
ALTER TABLE workspaces ADD COLUMN node_id TEXT
    REFERENCES nodes(id) ON DELETE SET NULL;
CREATE INDEX workspaces_node_idx ON workspaces(node_id);
