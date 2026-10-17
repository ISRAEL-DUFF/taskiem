# Data tables

Records a business keeps (customers, debtors, staff, stock) that its workflows read and write, without a spreadsheet or a database of its own. A data table is a Postgres table in a Xenibase project that Taskiem creates and manages for the organisation ([decision 0033](decisions/0033-data-tables-on-xenibase.md)); workflows reach it through the `pgdock@1` connection named `data_tables` ([Xenibase integration](integrations/pgdock.md)).

This page is being written with the feature. It will cover: creating a table and its columns, the table page, rows by API and CSV, using a table in a workflow, the `row_changed` trigger on a table, data tables in WhatsApp and the AI builder, permissions (`table.read`, `table.edit`), personal data, limits, and operator setup (`TASKIEM_XENIBASE_URL`, `TASKIEM_XENIBASE_TOKEN`, `TASKIEM_XENIBASE_ORG`).
