package main

import (
	"encoding/json"
	"log"
	"os"

	"github.com/grovetools/agentlogs/config"
	"github.com/invopop/jsonschema"
)

func main() {
	r := &jsonschema.Reflector{
		AllowAdditionalProperties: true,
		ExpandedStruct:            true,
		FieldNameTag:              "yaml",
	}

	schema := r.Reflect(&config.Config{})
	schema.Title = "Grove Agent Logs (aglogs) Configuration"
	schema.Description = "Schema for the 'aglogs' extension in grove.yml."

	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		log.Fatalf("Error marshaling schema: %v", err)
	}

	if err := os.WriteFile("aglogs.schema.json", data, 0o644); err != nil {
		log.Fatalf("Error writing schema file: %v", err)
	}

	log.Printf("Successfully generated aglogs schema at aglogs.schema.json")
}
