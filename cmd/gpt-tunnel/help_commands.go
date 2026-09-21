package main

import (
	"fmt"
	"os"
)

func help(args []string) {
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "gpt-tunnel help does not accept arguments")
		os.Exit(2)
	}
	fmt.Print(`New project quick start

1. Clone the repository into a local or temporary folder:
   git clone <repository-url> <folder>
2. Change into the repository:
   cd <folder>
3. Save the existing airelay session as <folder>_worker using your normal host-side Airelay workflow.
4. Bind that saved relay to the project:
   gpt-tunnel project onboard AIR agentir_worker
5. The canonical Worker Agent identity is AIR-WORKER.
6. Copy the printed token into the project chat and call session_start with {token}; retrieve the same value later with gpt-tunnel session token AIR.

GPT Tunnel only binds the existing saved relay key. It does not automate Airelay trust, bypass approval, model selection, native-session creation or discovery, or runtime startup.

Commands

  gpt-tunnel project onboard <PROJECT_CODE> <WORKER_RELAY>
  gpt-tunnel project list
  gpt-tunnel project read <PROJECT_ID>
  gpt-tunnel project status <PROJECT_ID>
  gpt-tunnel session token <PROJECT_CODE>
  gpt-tunnel guide
  gpt-tunnel agent register --relay <SESSION>
  gpt-tunnel task <read|work|finalize|submit-code|submit-tests|submit-rebase> ...
  gpt-tunnel {format|check|test|verify|work|plan|adr|journal|git|query|daemon} ...
`)
}
