-- Parallel steps (spec 3.2).

-- +goose Up
-- StepCancelled: a step in a losing branch of a join "any" parallel step,
-- stopped before it finished.
ALTER TABLE run_events DROP CONSTRAINT run_events_type_check;
ALTER TABLE run_events ADD CONSTRAINT run_events_type_check CHECK (type IN (
  'RunStarted', 'RunAdmitted', 'StepScheduled', 'StepStarted', 'EffectIntent', 'StepCompleted',
  'StepFailed', 'StepSkipped', 'StepCancelled', 'RetryScheduled', 'TimerFired', 'SignalReceived',
  'ApprovalRequested', 'ApprovalDecided', 'CompensationStarted', 'CompensationCompleted',
  'RunCompleted', 'RunFailed', 'RunCancelled'));

-- +goose Down
ALTER TABLE run_events DROP CONSTRAINT run_events_type_check;
ALTER TABLE run_events ADD CONSTRAINT run_events_type_check CHECK (type IN (
  'RunStarted', 'RunAdmitted', 'StepScheduled', 'StepStarted', 'EffectIntent', 'StepCompleted',
  'StepFailed', 'StepSkipped', 'RetryScheduled', 'TimerFired', 'SignalReceived',
  'ApprovalRequested', 'ApprovalDecided', 'CompensationStarted', 'CompensationCompleted',
  'RunCompleted', 'RunFailed', 'RunCancelled'));
