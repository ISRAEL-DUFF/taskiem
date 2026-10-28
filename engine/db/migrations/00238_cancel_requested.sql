-- Cancelling a run reverses it (decision 0054).

-- +goose Up
-- CancelRequested: a person cancelled the run with its effects reversed;
-- decide ends it once its writes under way settle and its compensations ran.
ALTER TABLE run_events DROP CONSTRAINT run_events_type_check;
ALTER TABLE run_events ADD CONSTRAINT run_events_type_check CHECK (type IN (
  'RunStarted', 'RunAdmitted', 'StepScheduled', 'StepStarted', 'EffectIntent', 'StepCompleted',
  'StepFailed', 'StepSkipped', 'StepCancelled', 'RetryScheduled', 'TimerFired', 'SignalReceived',
  'ApprovalRequested', 'ApprovalDecided', 'CompensationStarted', 'CompensationCompleted',
  'CancelRequested', 'RunCompleted', 'RunFailed', 'RunCancelled'));

-- +goose Down
ALTER TABLE run_events DROP CONSTRAINT run_events_type_check;
ALTER TABLE run_events ADD CONSTRAINT run_events_type_check CHECK (type IN (
  'RunStarted', 'RunAdmitted', 'StepScheduled', 'StepStarted', 'EffectIntent', 'StepCompleted',
  'StepFailed', 'StepSkipped', 'StepCancelled', 'RetryScheduled', 'TimerFired', 'SignalReceived',
  'ApprovalRequested', 'ApprovalDecided', 'CompensationStarted', 'CompensationCompleted',
  'RunCompleted', 'RunFailed', 'RunCancelled'));
