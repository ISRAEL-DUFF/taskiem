// The one rule for typed numbers, and the words for codes: the same table
// as web/src/lib/phone.test.ts (PHONE_CASES). Keep the two the same.

import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/api/transport.dart';
import 'package:taskiem/app/phone.dart';

const phoneCases = <(String, String?)>[
  ('08031234567', '+2348031234567'),
  ('0803 123 4567', '+2348031234567'),
  ('0803-123-4567', '+2348031234567'),
  ('(0803) 123.4567', '+2348031234567'),
  ('8031234567', '+2348031234567'),
  ('7031234567', '+2347031234567'),
  ('9031234567', '+2349031234567'),
  ('+2348031234567', '+2348031234567'),
  ('+234 803 123 4567', '+2348031234567'),
  ('+23408031234567', '+2348031234567'),
  ('+234 0803 123 4567', '+2348031234567'),
  ('002348031234567', '+2348031234567'),
  ('0023408031234567', '+2348031234567'),
  ('+447911123456', '+447911123456'),
  ('00447911123456', '+447911123456'),
  ('+1 (415) 555-0100', '+14155550100'),
  ('+12345678', '+12345678'),
  ('+123456789012345', '+123456789012345'),
  ('+44 7700 900123', '+447700900123'),
  ('0803 123', null),
  ('+234 803 123', null),
  ('', null),
  ('hello', null),
  ('2348031234567', null),
  ('6031234567', null),
  ('803123456', null),
  ('080312345678', null),
  ('+2346031234567', null),
  ('+234803123456', null),
  ('+23480312345678', null),
  ('+1234567', null),
  ('+1234567890123456', null),
  ('+0441234567', null),
  ('+44 7911 12345a', null),
  ('++2348031234567', null),
];

void main() {
  for (final (typed, want) in phoneCases) {
    test('reads "$typed" as $want', () => expect(toE164(typed), want));
  }

  test('numbers read back grouped, as the web shows them', () {
    expect(showPhone('+2348031234567'), '+234 803 123 4567');
    expect(showPhone('+447700900123'), '+447700900123');
    expect(sentWords('whatsapp', '+2348031234567'), 'We sent a 6-digit code to WhatsApp on +234 803 123 4567.');
    expect(sentWords('sms', '+2348031234567'), 'We sent a 6-digit code by SMS to +234 803 123 4567.');
    expect(countdown(45), '0:45');
    expect(countdown(60), '1:00');
  });

  test('what went wrong, in the web’s plain words', () {
    expect(codeProblem(ApiError(429, {'error': 'x', 'locked': true})), 'Too many tries. Wait an hour and try again.');
    expect(codeProblem(ApiError(429, {'error': 'too many login attempts'})), 'Too many tries. Wait an hour and try again.');
    expect(codeProblem(ApiError(401, {'error': 'x', 'code_wrong': true})), 'That code is not right. Check it, or ask for a new one.');
    expect(codeProblem(ApiError(401, {'error': 'x', 'ticket_expired': true})), 'That code has expired: ask for a new one.');
    expect(codeProblem(ApiError(401, {'error': 'x', 'tries_left': 2})), 'That PIN is not right. 2 tries left.');
    expect(codeProblem(ApiError(401, {'error': 'x', 'tries_left': 1})), 'That PIN is not right. 1 try left.');
    expect(codeProblem(ApiError(401, {'error': 'this account is not active'})), 'This account can\'t sign in. Contact Taskiem.');
    expect(codeProblem(ApiError(400, {'error': 'x', 'phone_invalid': true})), 'That doesn\'t look like a phone number.');
    expect(codeProblem(ApiError(502, {'error': 'the code could not be sent'})), 'The code could not be sent.');
  });
}
