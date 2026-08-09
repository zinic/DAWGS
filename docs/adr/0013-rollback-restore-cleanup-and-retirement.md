# ADR 0013: Rollback, restore, cleanup, fleet, and late-upgrade policy

Status: accepted for implementation; operational evidence pending.

In-place rollback ends atomically at acceptance. Before then it reverses
recorded object maps in reverse order, recreates the frozen V1 contract, and
keeps V1 fenced until a preparation-role handoff receipt. Accepted recovery is
forward repair or restore/PITR followed by identity rebind and new smoke.
Cleanup consumes only an immutable accepted OID/fingerprint map and requires
current epoch/smoke, soak, fresh backup, restore rehearsal, fleet approval, and
an action-bound confirmation. Runtime V1 retirement, fleet completion, and
late-upgrade archive retirement are separate gates.
