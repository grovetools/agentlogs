package main

import (
	"encoding/json"
	"log"
	"os"

	"github.com/invopop/jsonschema"
	"github.com/mattsolo1/grove-agent-logs/config"
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

	if err := os.WriteFile("aglogs.schema.json", data, 0644); err != nil {
		log.Fatalf("Error writing schema file: %v", err)
	}

	log.Printf("Successfully generated aglogs schema at aglogs.schema.json")
}
