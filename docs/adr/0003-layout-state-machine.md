# ADR 0003: Layout state machine and authoritative pointers

Status: accepted for implementation; transition table tests required.

One singleton layout row and append-only receipts are authoritative. Legal
states are uninitialized, recognized V1, fence phases, migrating phases,
pending acceptance, pending smoke, accepted V2, and rolled-back V1 handoff
phases. Each state has exactly one compatible current operation pointer;
accepted scope and active migration pointers are mutually exclusive. Database
constraints and transition routines enforce the graph. Names, marker version,
or the presence of tables cannot advance state. Unknown combinations are hard
drift and fail closed.
