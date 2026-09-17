# Agent brief

Posted as a comment when an issue becomes `ready-for-agent`. The brief is the contract; the original text and discussion are context.

Durable over precise: the issue may wait for weeks. Describe interfaces, types and behaviour, never file paths or line numbers. Describe what the system should do, not how to change the code; the worker explores the code fresh. Every acceptance criterion is testable on its own. Say what is out of scope.

    ## Agent brief
    **Category:** bug | enhancement
    **Summary:** one line.

    **Current behaviour:** what happens now; for a bug, the broken behaviour, with the reproduction that confirmed it.
    **Desired behaviour:** what happens after the work, including edge cases and errors.
    **Key interfaces:** `TypeName`, `functionName()`, config shapes: what changes and why.
    **Acceptance criteria:**
    - [ ] one testable line each
    **Out of scope:** what the worker must not touch.
    **Docs:** glossary terms and ADRs to write, or none.
