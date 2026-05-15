# Task List (fixture)

| ID   | Title           | Dependencies | Spec / Plan Refs                       | Expected Paths                              |
|------|-----------------|--------------|----------------------------------------|---------------------------------------------|
| F01  | Done foundation | -            | docs/fake/f01.md                       | `internal/fake/f01/**`                      |
| F02  | Pending child   | F01          | docs/fake/f02.md, docs/fake/f02-b.md   | `internal/fake/f02/**`, `cmd/fake/f02.go`   |
| F03  | Failed sibling  | F01          | docs/fake/f03.md                       | `internal/fake/f03/**`                      |
| F04  | Blocked         | F02, F03     | docs/fake/f04.md                       | `internal/fake/f04/**`                      |
