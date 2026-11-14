-- A transfer moves money on both statements, so a document may appear once per line (R16).
ALTER TABLE bank_line_matches DROP CONSTRAINT bank_line_matches_org_id_doc_kind_doc_id_key;
ALTER TABLE bank_line_matches ADD CONSTRAINT bank_line_matches_line_doc_key UNIQUE (org_id, line_id, doc_kind, doc_id);
