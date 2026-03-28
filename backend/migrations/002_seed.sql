-- Seed data for SQL Seminar Platform
-- Passwords: teacher1 -> ibelova, admin -> admin, students -> student1

INSERT INTO groups (id, title, stream) VALUES
  ('group-pi-22', 'PI-22', 'DB Systems'),
  ('group-ds-11', 'DS-11', 'Data Science')
ON CONFLICT (id) DO NOTHING;

INSERT INTO users (id, full_name, login, password_hash, role, group_id, created_at) VALUES
  ('teacher-1','Dr. Irina Belova','ibelova','cde383eee8ee7a4400adf7a15f716f179a2eb97646b37e089eb8d6d04e663416','teacher',NULL,'2026-01-10T00:00:00Z'),
  ('admin-1','Platform Admin','admin','240be518fabd2724ddb6f04eeb1da5967448d7e831c08c8fa822809f74c720a9','admin',NULL,'2026-01-05T00:00:00Z'),
  ('student-1','Kirill Fadeev','kfadeev','703b0a3d6ad75b649a28adde7d83c6251da457549263bc7ff45ec709b0a8448b','student','group-pi-22','2026-02-01T00:00:00Z'),
  ('student-2','Alina Nazarova','anazarova','703b0a3d6ad75b649a28adde7d83c6251da457549263bc7ff45ec709b0a8448b','student','group-pi-22','2026-02-01T00:00:00Z'),
  ('student-3','Maksim Vetrov','mvetrov','703b0a3d6ad75b649a28adde7d83c6251da457549263bc7ff45ec709b0a8448b','student','group-pi-22','2026-02-01T00:00:00Z'),
  ('student-4','Elena Shapoval','eshapoval','703b0a3d6ad75b649a28adde7d83c6251da457549263bc7ff45ec709b0a8448b','student','group-pi-22','2026-02-01T00:00:00Z'),
  ('student-5','Pavel Morozov','pmorozov','703b0a3d6ad75b649a28adde7d83c6251da457549263bc7ff45ec709b0a8448b','student','group-pi-22','2026-02-01T00:00:00Z'),
  ('student-6','Yana Egorova','yegorova','703b0a3d6ad75b649a28adde7d83c6251da457549263bc7ff45ec709b0a8448b','student','group-pi-22','2026-02-01T00:00:00Z')
ON CONFLICT (id) DO NOTHING;
