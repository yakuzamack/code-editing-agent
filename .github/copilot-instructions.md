Use these instructions for work in this repository, especially when the task touches UTM validation, Windows guest automation, or the crypto-framework workspace.

Project context:

- This repo contains the `utm_validate` tool and related workflows for automating UTM-based Windows testing against `/Users/home/Projects/crypto-framework`.
- The default UTM VM is `Windows 2`.
- The usual host-side UTM share directory is `~/UTM-crypto-audit`.

UTM workflow rules:

- Prefer SSH-first execution for guest commands when working with the Windows VM. Treat WebDAV shares and AppleScript guest execution as fallback paths, not the default transport.
- When you need to target the current Windows VM, prefer host `192.168.1.244` and user `ayoub` unless the user provides newer values.
- Do not hard-code passwords, API keys, or other secrets into repository files. If the user provided a password in chat for the current session, use it only for the live task and avoid writing it into code, docs, or committed instructions.
- If SSH automation needs credentials, prefer environment variables or one-shot terminal usage over source changes.

Implementation guidance:

- When updating UTM automation, favor env-driven settings such as `CRYPTOFRAMEWORK_UTM_VM`, `CRYPTOFRAMEWORK_UTM_SHARE`, and SSH-specific variables over literals in scripts.
- Keep `utm_validate` aligned with the actual crypto-framework scripts. If the scripts move from AppleScript or WebDAV toward SSH, reflect that behavior in tool descriptions and orchestration logic.
- For build or deploy failures, verify transport assumptions first: VM reachability, SSH login, and command execution. Only fall back to AppleScript after SSH is shown unavailable.
- When validating claimed fixes, run the relevant command and report the real result. Do not state that tests pass unless the exact command completed successfully.

Editing guidance:

- Keep UTM changes minimal and transport-focused.
- Preserve existing public APIs unless the task explicitly requires changing them.
- If you add SSH support, make the fallback order explicit in comments and code paths.
