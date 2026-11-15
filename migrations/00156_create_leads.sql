-- +goose Up

-- A consultation request sent from the landing site's form (T-R11.6, D18): what
-- the person typed, the consent it carried and how the centre handled it. This
-- is personal data held outside app.users, given with consent for one purpose,
-- a call back, so the row holds nothing the form did not ask for: no IP
-- address and nothing about the device. privacy_notice_version and
-- consented_at say what the person agreed to and when. The retention sweep
-- deletes a row 12 months after created_at, and an Admin deletes one sooner
-- when the person asks.
--
-- phone_digits is the digits of the number as typed, so one number written two
-- ways is one value: the form's cap on repeats and the Admin's search compare
-- it. interests is a short list of checked slugs rather than a child table,
-- because it is read and written whole and never joined. The enumerations are
-- text with a CHECK, as notifications.kind is (D-28).
--
-- The foreign key locks app.users while the file runs, so lock_timeout fails it
-- at once rather than queue writers behind it.
SET LOCAL lock_timeout = '5s';

CREATE TABLE app.leads (
  id                     uuid PRIMARY KEY DEFAULT uuidv7(),
  full_name              text NOT NULL,
  phone_digits           text NOT NULL,
  interests              text[] NOT NULL DEFAULT '{}',
  course_for             text NOT NULL,
  call_time              text NOT NULL,
  locale                 text NOT NULL,
  privacy_notice_version text NOT NULL,
  consented_at           timestamptz NOT NULL,
  status                 text NOT NULL DEFAULT 'new',
  note                   text,
  handled_by             uuid REFERENCES app.users(id) ON DELETE SET NULL,
  handled_at             timestamptz,
  created_at             timestamptz NOT NULL DEFAULT now(),
  updated_at             timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT leads_full_name_check
    CHECK (char_length(full_name) BETWEEN 1 AND 120 AND full_name = btrim(full_name)),
  CONSTRAINT leads_phone_digits_check
    CHECK (phone_digits ~ '^[0-9]{9,15}$'),
  CONSTRAINT leads_interests_check
    CHECK (
      cardinality(interests) <= 5
      AND interests <@ ARRAY['ielts', 'toeic', 'communication', 'kids', 'business']
    ),
  CONSTRAINT leads_course_for_check
    CHECK (course_for IN ('self', 'child', 'company')),
  CONSTRAINT leads_call_time_check
    CHECK (call_time IN ('any', 'morning', 'afternoon', 'evening')),
  CONSTRAINT leads_locale_check
    CHECK (locale IN ('vi', 'en')),
  CONSTRAINT leads_privacy_notice_version_check
    CHECK (char_length(privacy_notice_version) BETWEEN 1 AND 20),
  CONSTRAINT leads_status_check
    CHECK (status IN ('new', 'contacted', 'closed')),
  CONSTRAINT leads_note_check
    CHECK (char_length(note) BETWEEN 1 AND 1000)
);

-- The Admin's list, newest first, and the retention sweep's cutoff.
CREATE INDEX leads_created_at_idx ON app.leads (created_at DESC);
-- The list narrowed to one status, and the count of new requests in the nav.
CREATE INDEX leads_status_created_at_idx ON app.leads (status, created_at DESC);
-- The form's cap: the requests one number sent in the last 24 hours.
CREATE INDEX leads_phone_digits_created_at_idx ON app.leads (phone_digits, created_at DESC);
-- The cascade from app.users sets handled_by NULL by equality.
CREATE INDEX leads_handled_by_idx ON app.leads (handled_by)
  WHERE handled_by IS NOT NULL;

CREATE TRIGGER leads_set_updated_at BEFORE UPDATE ON app.leads
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();

-- quizzivy_app holds SELECT, INSERT, UPDATE and DELETE through the default
-- privileges of 00009. retention-sweep deletes the rows past 12 months as
-- quizzivy_maintenance (D16): its DELETE reads id and created_at to find them,
-- so DELETE alone fails with 42501, and it is granted those two columns and
-- nothing a person typed (00139's rule).
GRANT SELECT (id, created_at), DELETE ON app.leads TO quizzivy_maintenance;

-- +goose Down
SET LOCAL lock_timeout = '5s';

DROP TABLE app.leads;
