# How does communication between the CLI and the Agent work?

Disclaimer: Looked briefly at bidirectional communication with Hashicorp plugin model but tabled that for now
https://github.com/hashicorp/go-plugin/tree/main/examples/bidirectional

Goal here is to make the API somewhat clean to that we can move to this model later if needed.

### Terminology

Since the CLI is the one that initiates the communication, we will call it the `client` and the agent the `server`.

### Flows

- Client asks server if communication is required for a given channel
- If no, client proceeds with the command
- If yes, client start a communication session with the server until the server is satisfied

### Communication session

- Client sends an initial Information request to the server
- Server sends back an InformationRequest
- Client processes the InformationRequest with a handler (CLI prompt) and returns an Answer
- The Answer is sent back to the server
- If satisfied, the server communicates back that it is done
- Otherwise, we repeat the process until the server is satisfied
