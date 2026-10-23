DROP TABLE search_call;
ALTER TABLE agent DROP FOREIGN KEY fk_agent_search_provider, DROP COLUMN search_provider_id;
DROP TABLE search_config;
DROP TABLE search_provider;
