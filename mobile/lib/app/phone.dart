// Phone numbers and codes in plain words (docs/spec/taskiem-for-everyone.md
// §5.1, §5.5): the same rules and words as the web's web/src/lib/phone.ts.
// The tests share one table of numbers (test/phone_test.dart and
// web/src/lib/phone.test.ts): keep the two the same.

import '../api/transport.dart';

/// The country a number without one is in: Nigeria.
const homeCountry = '234';

/// What a number that cannot be one is told, before anything is sent.
const notAPhone = 'That doesn\'t look like a phone number.';

/// A code that has run out (the server's `ticket_expired`).
const codeExpired = 'That code has expired: ask for a new one.';

/// The way in's words when its code ran out while the name was typed.
const yourCodeExpired = 'Your code has expired: ask for a new one.';

/// Too many tries: a number locked, or too many asked for.
const tooMany = 'Too many tries. Wait an hour and try again.';

/// An account that is turned off, after a right code.
const accountOff = 'This account can\'t sign in. Contact Taskiem.';

/// Seconds before a new code can be asked for (the web's RESEND_AFTER).
const resendAfter = 60;

final _ignored = RegExp(r'[\s\-.() ]');

/// A typed number as `+<country><number>`, the way the server reads it:
/// spaces, dashes, dots and brackets ignored; a leading 00 is +; +234 then
/// 0 drops the 0; a local 0 and 10 digits, or a bare 10 digits starting 7,
/// 8 or 9, is Nigerian. A Nigerian number is +234 and 10 digits starting 7,
/// 8 or 9; another country's is + and 8 to 15 digits in all. Null when it
/// cannot be one.
String? toE164(String typed) {
  var s = typed.trim().replaceAll(_ignored, '');
  if (s.startsWith('00')) s = '+${s.substring(2)}';
  if (s.startsWith('+')) {
    if (!RegExp(r'^\+\d+$').hasMatch(s)) return null;
    if (s.startsWith('+${homeCountry}0')) s = '+$homeCountry${s.substring(homeCountry.length + 2)}';
  } else if (RegExp(r'^0\d{10}$').hasMatch(s)) {
    s = '+$homeCountry${s.substring(1)}';
  } else if (RegExp(r'^[789]\d{9}$').hasMatch(s)) {
    s = '+$homeCountry$s';
  } else if (RegExp(r'^[1-9]\d+$').hasMatch(s)) {
    s = '+$s';
  } else {
    return null;
  }
  if (s.startsWith('+$homeCountry')) return RegExp(r'^\+234[789]\d{9}$').hasMatch(s) ? s : null;
  return RegExp(r'^\+[1-9]\d{7,14}$').hasMatch(s) ? s : null;
}

/// A number to read back: Nigerian numbers grouped as +234 803 123 4567.
String showPhone(String e164) {
  final m = RegExp(r'^\+234(\d{3})(\d{3})(\d{4})$').firstMatch(e164);
  return m == null ? e164 : '+234 ${m[1]} ${m[2]} ${m[3]}';
}

/// Where the code went, in a sentence.
String sentWords(String via, String phone) {
  final to = showPhone(phone);
  return via == 'sms' ? 'We sent a 6-digit code by SMS to $to.' : 'We sent a 6-digit code to WhatsApp on $to.';
}

/// Seconds as "0:45", for when a new code can be asked for.
String countdown(int s) {
  final n = s < 0 ? 0 : s;
  return '${n ~/ 60}:${(n % 60).toString().padLeft(2, '0')}';
}

/// A wrong chat PIN, and how many tries are left.
String pinWrong(int left) => left <= 0 ? tooMany : 'That PIN is not right. $left ${left == 1 ? 'try' : 'tries'} left.';

/// A server's own sentence, made to read as one: a capital first.
String sentence(String s) {
  final t = s.trim();
  if (t.isEmpty) return 'Something went wrong. Try again.';
  final out = '${t[0].toUpperCase()}${t.substring(1)}';
  return RegExp(r'[.!?:]$').hasMatch(out) ? out : '$out.';
}

/// What went wrong with a code (or a PIN), in plain words.
String codeProblem(ApiError e) {
  final left = e.body['tries_left'];
  if (e.has('locked') || e.status == 429) return tooMany;
  if (e.has('sms_unavailable')) return 'We can\'t send codes by SMS here. Ask for the code on WhatsApp instead.';
  if (e.has('phone_signin_off')) return 'Signing in with a code to your phone is turned off for this account. Sign in with your email instead.';
  if (e.has('phone_invalid')) return notAPhone;
  if (e.has('ticket_expired')) return codeExpired;
  if (e.has('code_wrong')) return 'That code is not right. Check it, or ask for a new one.';
  if (left is num) return pinWrong(left.toInt());
  if (e.has('account_off') || (e.status == 401 && RegExp('not active|disabled', caseSensitive: false).hasMatch(e.message))) return accountOff;
  if (e.status == 503) return 'We can\'t send codes to phones just now. Sign in with your email instead.';
  if (e.status == 400) return notAPhone;
  return sentence(e.message);
}
