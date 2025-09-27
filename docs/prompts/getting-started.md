Create a "Getting Started" guide for the `clogs` CLI. This should be a short tutorial that walks a new user through a common workflow.

1. Start by explaining how to list all available Claude sessions with `clogs list`. Show example output.
2. Show how to filter the list for a specific project using `clogs list --project <project-name>`.
3. Demonstrate how to read the transcript for a specific task using `clogs read <plan/job.md>`. Explain what a "plan/job" is by referencing the `parsePlanInfo` function in `main.go`.
4. Show how to inspect the raw messages from a session using `clogs query <session_id> --role assistant`.
5. Finally, explain how to follow a live session with `clogs tail <session_id>`.