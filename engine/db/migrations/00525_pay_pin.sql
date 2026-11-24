-- The PIN as the fallback to a passkey for confirming a payment
-- (docs/spec/chat-pay-people.md, Settled row 3; api/payments_pin.go). The
-- PIN is the person's one PIN (whatsapp_pins), entered only in the app's
-- own pad or WhatsApp's secure form. A payment confirmed with it says so
-- (confirmed_with), so the PIN's own day's total, across every business
-- the person pays for, can be counted; and the PIN form sent for one
-- payment is a whatsapp_flows row of its own kind, the payment's id in its
-- data. The PIN's set_at (00061) already says when it was set or changed.
-- +goose Up
ALTER TABLE payments ADD COLUMN confirmed_with text CHECK (confirmed_with IN ('passkey', 'pin'));
CREATE INDEX payments_pin_day ON payments (confirmed_by, confirmed_at) WHERE confirmed_with = 'pin';

-- Only the PIN's own payments, only the columns its day's total reads (no
-- payee, account, purpose or note), and only through the function below.
CREATE POLICY pin_day ON payments FOR SELECT TO taskiem_dispatch USING (confirmed_with = 'pin');
GRANT SELECT (amount_kobo, confirmed_with, confirmed_by, confirmed_at, status) ON payments TO taskiem_dispatch;

-- What a person confirmed with their PIN today (Lagos's day, as pay
-- limits count it), in every business: sent, waiting, paid or unknown.
-- +goose StatementBegin
CREATE FUNCTION taskiem_pin_paid_today(p_user uuid)
RETURNS bigint LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT COALESCE(sum(amount_kobo), 0)::bigint FROM payments
   WHERE confirmed_with = 'pin' AND confirmed_by = p_user::text
     AND status IN ('sending', 'waiting_ok', 'paid', 'unknown')
     AND confirmed_at > date_trunc('day', now() AT TIME ZONE 'Africa/Lagos') AT TIME ZONE 'Africa/Lagos'
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_pin_paid_today(uuid) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_pin_paid_today(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_pin_paid_today(uuid) TO taskiem_app;

-- The PIN form for one payment.
ALTER TABLE whatsapp_flows DROP CONSTRAINT whatsapp_flows_kind_check;
ALTER TABLE whatsapp_flows ADD CONSTRAINT whatsapp_flows_kind_check CHECK (kind IN ('inputs', 'pin', 'public', 'pin_set', 'pin_wake', 'connect', 'pay_pin'));

-- +goose Down
DELETE FROM whatsapp_flows WHERE kind = 'pay_pin';
ALTER TABLE whatsapp_flows DROP CONSTRAINT whatsapp_flows_kind_check;
ALTER TABLE whatsapp_flows ADD CONSTRAINT whatsapp_flows_kind_check CHECK (kind IN ('inputs', 'pin', 'public', 'pin_set', 'pin_wake', 'connect'));
DROP FUNCTION taskiem_pin_paid_today(uuid);
REVOKE SELECT (amount_kobo, confirmed_with, confirmed_by, confirmed_at, status) ON payments FROM taskiem_dispatch;
DROP POLICY pin_day ON payments;
DROP INDEX payments_pin_day;
ALTER TABLE payments DROP COLUMN confirmed_with;
