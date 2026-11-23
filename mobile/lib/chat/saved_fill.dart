// A form's Saved button (saved objects S2, docs/spec/saved-objects.md §6.4):
// "Anywhere a form asks for an address, a phone, a person, a bank account
// or a date, a Saved button offers the owner's objects of the right kind",
// the business's and the person's own, one picked to fill it in.

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../app/auth.dart';
import '../app/passkey.dart' show PasskeyCancelled;
import '../kit/kit.dart' show TkSmallButton;
import '../kit/modal.dart' show showTkToast;
import 'groups.dart' show pickOneOf;
import 'my_chat.dart' show sayFailure;
import 'saved_accounts.dart' show SavedApi, savedKinds, savedShortHint;

/// The Saved button beside (or over) the field it fills: "Loading…" and
/// not pressed again while its list is read and picked from ([picking]),
/// nor while the form is saving ([busy]).
class SavedButton extends StatelessWidget {
  const SavedButton({super.key, required this.semantics, required this.onPressed, this.picking = false, this.busy = false, this.label = 'Saved'});
  final String semantics;
  final VoidCallback onPressed;
  final bool picking;
  final bool busy;
  final String label;

  @override
  Widget build(BuildContext context) =>
      TkSmallButton(label: picking ? 'Loading…' : label, semantics: picking ? '$semantics: loading' : semantics, onPressed: picking || busy ? null : onPressed);
}

/// The person's own saved things and, in a business, the business's, both
/// read at once and each on its own: one list that cannot be read leaves
/// the other. [fits] keeps those of the kind asked. Null, said in a toast,
/// when neither can be read, or nothing read is of the kind.
Future<({List<SavedObject> all, bool both})?> readSaved(BuildContext context, Auth auth, {String? kind, required bool Function(SavedObject x) fits}) async {
  Object? failed;
  Future<List<SavedObject>?> read(Future<List<SavedObject>> Function() f) async {
    try {
      return await f();
    } on Object catch (e) {
      failed ??= e;
      return null;
    }
  }

  final lists = await Future.wait([
    read(() async => (await auth.api.listMySaved(kind: kind)).saved),
    if (!auth.personal) read(() async => (await auth.api.listSaved(kind: kind)).saved),
  ]);
  if (!context.mounted) return null;
  if (lists.every((l) => l == null)) {
    showTkToast(context, sayFailure(failed!), error: true);
    return null;
  }
  final mine = [for (final x in lists[0] ?? const <SavedObject>[]) if (fits(x)) x];
  final business = [for (final x in lists.length > 1 ? lists[1] ?? const <SavedObject>[] : const <SavedObject>[]) if (fits(x)) x];
  final all = [...mine, ...business];
  if (all.isEmpty) {
    showTkToast(context, failed == null ? 'Nothing saved of this kind yet' : '${sayFailure(failed!)} Nothing of this kind in what could be read.', error: failed != null);
    return null;
  }
  return (all: all, both: mine.isNotEmpty && business.isNotEmpty);
}

/// The line under a saved thing in a picker: its @name, hint, and whose
/// when both the person and the business have some.
String savedPickSub(SavedObject x, bool both, [String? hint]) =>
    ['@${x.name}', hint ?? x.hint, if (both) x.owner == 'mine' ? 'Yours' : 'The business’s'].where((w) => w.isNotEmpty).join(' · ');

String _key(SavedObject x) => '${x.owner}:${x.id}';

String _titleOf(SavedObject x) => x.title.isEmpty ? '@${x.name}' : x.title;

/// One of the saved things of [kind] (and that [fits]), the person's own
/// first, then the business's (none for a person with no business), each
/// by its title with its @name and hint; null when none is picked.
Future<SavedObject?> pickSaved(BuildContext context, Auth auth, {required String kind, required String title, bool Function(SavedObject x)? fits}) async {
  final got = await readSaved(context, auth, kind: kind, fits: (x) => x.kind == kind && (fits?.call(x) ?? true));
  if (got == null || !context.mounted) return null;
  final key = await pickOneOf(
    context,
    title: title,
    searchHint: got.all.length > 6 ? 'Search by name' : null,
    icon: savedKinds[kind]?.icon ?? 'b-note',
    choices: [for (final x in got.all) (_key(x), _titleOf(x), savedPickSub(x, got.both))],
  );
  return got.all.where((x) => _key(x) == key).firstOrNull;
}

// --- a phone ---

/// A saved person's or place's phone, in full; null when none is picked.
Future<String?> pickSavedPhone(BuildContext context, Auth auth) async {
  final got = await readSaved(context, auth, fits: (x) => (x.kind == 'person' || x.kind == 'place') && (x.details.phone ?? '').trim().isNotEmpty);
  if (got == null || !context.mounted) return null;
  final key = await pickOneOf(
    context,
    title: 'Saved phone numbers',
    searchHint: got.all.length > 6 ? 'Search by name' : null,
    icon: 'b-phone',
    choices: [for (final x in got.all) (_key(x), _titleOf(x), savedPickSub(x, got.both))],
  );
  return got.all.where((x) => _key(x) == key).firstOrNull?.details.phone?.trim();
}

// --- a place ---

/// A saved place as an address line: "12 Admiralty Way, Lekki (by the
/// filling station)"; the landmark alone when there is no street or area.
String placeLine(SavedObjectDetails d) => placeFit(d).line;

/// A saved place as an address line of at most [max] characters (the
/// server's limit): the landmark left out to fit, then the area, then the
/// line cut. [dropped] says what was left out ('' for nothing).
({String line, String dropped}) placeFit(SavedObjectDetails d, {int max = 1 << 30}) {
  final street = (d.line ?? '').trim(), area = (d.area ?? '').trim(), mark = (d.landmark ?? '').trim();
  String join(List<String> parts, String mark) {
    final line = parts.where((w) => w.isNotEmpty).join(', ');
    if (mark.isEmpty) return line;
    return line.isEmpty ? mark : '$line ($mark)';
  }

  final whole = join([street, area], mark);
  if (whole.length <= max) return (line: whole, dropped: '');
  final noMark = join([street, area], '');
  if (mark.isNotEmpty && noMark.isNotEmpty && noMark.length <= max) return (line: noMark, dropped: 'the landmark');
  if (street.isNotEmpty && area.isNotEmpty && street.length <= max) return (line: street, dropped: mark.isEmpty ? 'the area' : 'the landmark and the area');
  final base = street.isNotEmpty ? street : (noMark.isNotEmpty ? noMark : whole);
  return (line: base.substring(0, max).trimRight(), dropped: 'the end of the address');
}

/// A saved place picked, as an address of at most [max] characters; what
/// was left out to fit is said in a toast. Null when none is picked.
Future<({String address, String phone})?> pickSavedPlace(BuildContext context, Auth auth, {int max = 300}) async {
  final x = await pickSaved(context, auth, kind: 'place', title: 'Saved places');
  if (x == null || !context.mounted) return null;
  final fit = placeFit(x.details, max: max);
  if (fit.dropped.isNotEmpty) showTkToast(context, 'Left out ${fit.dropped}: an address here is up to $max characters.');
  return (address: fit.line, phone: (x.details.phone ?? '').trim());
}

// --- a bank account ---

/// A saved bank account picked: its bank, and its number in full once a
/// passkey shows it. A passkey put away, or none on this phone, fills only
/// the bank ([number] empty), and says so.
typedef SavedAccountFill = ({String bankName, String bankCode, String codeFrom, String number, String accountName});

Future<SavedAccountFill?> pickSavedAccount(BuildContext context, Auth auth) async {
  final x = await pickSaved(context, auth, kind: 'bank_account', title: 'Saved accounts');
  if (x == null || !context.mounted) return null;
  final bank = (x.details.bankName ?? '').isNotEmpty ? x.details.bankName! : savedShortHint(x);
  final name = x.check.accountName is String ? x.check.accountName as String : '';
  var number = '';
  try {
    number = (await SavedApi(auth, x.owner == 'mine').reveal(x.id)).number;
  } on PasskeyCancelled {
    if (context.mounted) showTkToast(context, 'Only the bank is filled: the number needs your passkey.');
  } on Object catch (e) {
    if (context.mounted) showTkToast(context, '${sayFailure(e)} Only the bank is filled.', error: true);
  }
  return (bankName: bank, bankCode: x.details.bankCode ?? '', codeFrom: x.details.codeFrom ?? '', number: number, accountName: name);
}

/// A saved bank account into a form's typed bank, number and name: the
/// whole account, so nothing of the one there before stays (a number not
/// shown, or a name the bank has not given, is left empty to type).
Future<void> fillSavedAccount(
  BuildContext context,
  Auth auth, {
  required TextEditingController bank,
  required TextEditingController number,
  TextEditingController? name,
}) async {
  final a = await pickSavedAccount(context, auth);
  if (a == null || !context.mounted) return;
  bank.text = a.bankName;
  number.text = a.number;
  name?.text = a.accountName;
}

/// The code of [bank] in a list of banks ({code, name}): its own code when
/// the list has it under that name, else by the bank's name ("Bank", "PLC"
/// and case set aside); '' when the list has no such bank.
String bankCodeIn(List<({String code, String name})> banks, SavedAccountFill bank) {
  String key(String s) => s.toLowerCase().split(RegExp('[^a-z0-9]+')).where((w) => w.isNotEmpty && !const {'bank', 'plc', 'limited', 'ltd'}.contains(w)).join(' ');
  for (final b in banks) {
    if (b.code == bank.bankCode && key(b.name) == key(bank.bankName)) return b.code;
  }
  for (final b in banks) {
    if (key(b.name) == key(bank.bankName)) return b.code;
  }
  return '';
}

// --- a date ---

const _mon = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

/// One date saved on a thing: "@car · Insurance due · 3 Mar 2027".
typedef SavedDate = ({SavedObject x, String label, String day});

String _ymd(DateTime d) => '${d.year.toString().padLeft(4, '0')}-${d.month.toString().padLeft(2, '0')}-${d.day.toString().padLeft(2, '0')}';

/// "3 Mar 2027" of "2027-03-03".
String savedDayWords(String day) {
  final d = DateTime.tryParse(day);
  return d == null ? day : '${d.day} ${_mon[d.month - 1]} ${d.year}';
}

/// The dates on [things]: each made-up kind's date fields (by [kinds]) and
/// a person's next birthday after [today]. A sensitive date, masked
/// ("••"), is left out: it is not shown in a list.
List<SavedDate> savedDates(List<SavedObject> things, List<SavedKind> kinds, DateTime today) {
  String? day(Object? v) {
    if (v is! String || v.contains('•')) return null;
    final d = DateTime.tryParse(v.trim());
    return d == null ? null : _ymd(d);
  }

  final out = <SavedDate>[];
  for (final x in things) {
    if (x.kind == 'person') {
      final b = day(x.details.birthday);
      if (b == null) continue;
      final born = DateTime.parse(b);
      var next = DateTime(today.year, born.month, born.day);
      if (next.isBefore(DateTime(today.year, today.month, today.day))) next = DateTime(today.year + 1, born.month, born.day);
      out.add((x: x, label: 'Birthday', day: _ymd(next)));
    } else if (x.kind == 'custom') {
      final k = kinds.where((k) => k.id == x.kindId).firstOrNull;
      for (final f in k?.fields ?? const <SavedKindField>[]) {
        if (f.type != 'date' || f.key == null || f.sensitive == true || f.neverShare == true) continue;
        final d = day(x.values?[f.key]);
        if (d != null) out.add((x: x, label: f.label, day: d));
      }
    }
  }
  return out;
}

/// A date saved on one of the owner's things (as 2027-03-03); null when
/// none is picked.
Future<String?> pickSavedDate(BuildContext context, Auth auth, {DateTime? now}) async {
  Future<List<SavedKind>> kinds(Future<SavedKindList> Function() f) async {
    try {
      return (await f()).kinds;
    } on Object {
      return const [];
    }
  }

  final kindLists = Future.wait([kinds(auth.api.listMySavedKinds), if (!auth.personal) kinds(auth.api.listSavedKinds)]);
  final got = await readSaved(context, auth, fits: (x) => x.kind == 'person' || x.kind == 'custom');
  final all = [for (final l in await kindLists) ...l];
  if (got == null || !context.mounted) return null;
  final dates = savedDates(got.all, all, now ?? DateTime.now())..sort((a, b) => a.day.compareTo(b.day));
  if (dates.isEmpty) {
    showTkToast(context, 'No dates saved yet');
    return null;
  }
  final both = dates.any((d) => d.x.owner == 'mine') && dates.any((d) => d.x.owner != 'mine');
  final key = await pickOneOf(
    context,
    title: 'Saved dates',
    searchHint: dates.length > 6 ? 'Search' : null,
    icon: 'b-calendar',
    choices: [
      for (final (i, d) in dates.indexed) ('$i', '@${d.x.name} · ${d.label} · ${savedDayWords(d.day)}', [_titleOf(d.x), if (both) d.x.owner == 'mine' ? 'Yours' : 'The business’s'].join(' · ')),
    ],
  );
  final i = int.tryParse(key ?? '');
  return i == null ? null : dates[i].day;
}
