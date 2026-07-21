# Task: Generate Go Models and SQLite Schema

Based on the MyCFC project context, generate the Go structs and the `init.sql` file for the following entities:

1. **User**: ID, Name, Email, Role (Enum), Squad/Category (e.g., Polo, Master A, Iniciante), GuardianID (self-referencing foreign key for youth members).
2. **Event**: ID, Title, Date, Discipline (Kayak, Polo, SUP, Canoe), Type (Competition, Leisure, Maintenance), Description.
3. **Equipment**: ID, Name (e.g., Boat Polo #12, Van 1), Type (Boat, Paddle, Vehicle), Status (Operational, Maintenance).
4. **RepairRequest**: ID, EquipmentID, ReportedByID, IssueDescription, Status (Pending, In Progress, Resolved), DateReported.

Requirements:
* Use appropriate struct tags for JSON (if needed for API bridging later) and DB mapping.
* Write a simple SQLite schema that corresponds exactly to these structs.
