// Pay from a message (paying people, phase 2; docs/spec/chat-pay-people.md
// "Pay one person", "Three ways in"): a message in one of the business's
// chats that holds a 10-digit account number gets a "Pay this account"
// chip under it, for those who may pay (money.pay). The words are checked
// here first (ten digits together), then read by Taskiem (POST
// /v1/payments/read): the number, the bank, the amount and the name the
// message gives, the business's saved account with that number, and its
// last payment to it. Tapping opens the Pay sheet filled in: the bank and
// number for an owner, the saved account for staff (who pay saved accounts
// only). A chip already paid says so, and pays again. An account not
// saved yet has "Save as…" beside it, for those who may manage the
// business's saved things (pay_save.dart): once saved, the chip says
// "Pay @name" and pays by it. "Add to the list" beside it keeps the
// reading on the person's list of people to pay (pay_collect.dart).

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart' show ApiError;
import '../app/auth.dart';
import '../kit/icon.dart' show TkIcon;
import '../kit/kit.dart' show TkPress;
import '../kit/tap_area.dart' show TapArea;
import '../kit/modal.dart' show showTkToast;
import '../kit/sheet.dart' show showTkSheet;
import '../theme/look.dart';
import '../theme/theme.dart';
import 'menu_card.dart' show catalogueOf;
import 'my_chat.dart' show sayFailure;
import 'pay_collect.dart' show MessageFrom, addReadingToList;
import 'pay_save.dart' show PaySaveSheet, PaySaved, maySaveFromMessages;
import 'pay_sheet.dart' show PaySheet, noAccountInPhoto;
import 'photo_text.dart' show PhotoAI, noWordsInPhoto, readChatPhoto;
import 'words.dart' show naira;

/// Ten digits together (a space or a dash between them allowed), not
/// part of a longer number: what may be an account number.
final _tenDigits = RegExp(r'(?<![\d])\d(?:[ -]?\d){9}(?![\d])');

/// Whether the words may hold an account number: the quick check before
/// Taskiem reads them.
bool mayHoldAccount(String? text) => text != null && _tenDigits.hasMatch(text);

/// Whether this person is shown the chip: a business's, with money.pay.
bool mayPayFromMessages(Auth auth) => auth.me?.personal != true && auth.can('money.pay');

/// What Taskiem read of each message, by its id, kept while the app runs:
/// a thread scrolled or drawn again does not read it again.
final _reads = Expando<Map<String, Future<ReadPaymentResponse?>>>();

Future<ReadPaymentResponse?> _read(Auth auth, String id, String text) {
  final kept = _reads[auth] ??= {};
  return kept[id] ??= auth.api.readPayment(body: ReadPaymentRequest(text: text)).then<ReadPaymentResponse?>((r) => r.found ? r : null).catchError((Object e) {
    // Too many read just now: no chip, and not asked again for this
    // message; anything else is asked again when it is next drawn.
    if (!(e is ApiError && e.status == 429)) kept.remove(id);
    return null;
  });
}

const _months = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

/// "Paid ₦45,000 on 3 Nov" (Lagos's day); "Sent" while the bank has not
/// said, "Waiting for an OK" over a limit.
String paidWords(ReadPaymentResponsePaid p) {
  final at = p.at?.toUtc().add(const Duration(hours: 1));
  final day = at == null ? '' : ' on ${at.day} ${_months[at.month - 1]}';
  final amount = naira(p.amountKobo ?? 0);
  return switch (p.status) {
    'paid' => 'Paid $amount$day',
    'waiting_ok' => 'Sent $amount for an OK$day',
    _ => 'Sent $amount$day',
  };
}

/// Naira as the Pay sheet's box takes it: 45000, or 450.50.
String _nairaTyped(int kobo) => kobo % 100 == 0 ? '${kobo ~/ 100}' : (kobo / 100).toStringAsFixed(2);

/// The catalogue's Pay forms this person may use (those with a passkey):
/// by account number (owners), and by the business's saved account.
typedef PayForms = ({ChatAction? account, ChatAction? person});

Future<PayForms> payFormsOf(Auth auth) async {
  final topics = await catalogueOf(auth).catchError((Object _) => <ChatTopic>[]);
  ChatAction? find(String id) => [for (final t in topics) ...t.actions].where((a) => a.id == id && a.confirm == 'passkey').firstOrNull;
  return (account: find('pay.account'), person: find('pay.person'));
}

/// Said when staff are shown an account not saved yet.
const askToSave = 'Ask an owner to save this account: then you can pay it.';

/// The Pay sheet's form for what Taskiem read ([r]), and what it starts
/// with: the number, bank and amount by account number for an owner; the
/// business's saved account with that number, by its @name, for staff
/// (who pay saved accounts only), or when [byName] (saved just now). Null
/// when neither can pay it (staff, and the account not saved).
({ChatAction action, Map<String, String> initial})? payFromReading(PayForms forms, ReadPaymentResponse r, {bool byName = false}) {
  final amount = (r.amountKobo ?? 0) >= 100 ? _nairaTyped(r.amountKobo!) : '';
  final payee = (r.payee ?? '').trim();
  Map<String, String> byPayee(ChatAction action) => {
    for (final f in action.fields ?? const <ChatField>[])
      if (f.kind == 'payee') f.key: '@$payee' else if (f.kind == 'money' && amount.isNotEmpty) f.key: amount,
  };
  if (byName && forms.person != null && payee.isNotEmpty) return (action: forms.person!, initial: byPayee(forms.person!));
  if (forms.account case final a?) {
    return (
      action: a,
      initial: {
        for (final f in a.fields ?? const <ChatField>[])
          if (f.kind == 'account')
            f.key: r.accountNumber ?? ''
          else if (f.kind == 'bank' && (r.bankName ?? '').isNotEmpty)
            f.key: r.bankName!
          else if (f.kind == 'money' && amount.isNotEmpty)
            f.key: amount,
      },
    );
  }
  if (forms.person != null && payee.isNotEmpty) return (action: forms.person!, initial: byPayee(forms.person!));
  return null;
}

/// "Pay this account" on a photo in a chat ([path], where the chat
/// fetches it): read on the phone, the words read by Taskiem (`from:
/// "photo"`), and the Pay sheet opened filled in, as the chip under a
/// message opens it. Nothing found: said, or, where the business allows
/// it, "Read it with AI" offered, and its words read the same way.
Future<void> payFromChatPhoto(BuildContext context, Auth auth, String path) => _fromChatPhoto(context, auth, path, (r, forms) async {
  final fill = payFromReading(forms, r);
  if (fill == null) {
    showTkToast(context, askToSave);
    return;
  }
  await showTkSheet<Payment>(
    context,
    (_) => PaySheet(auth: auth, action: fill.action, initial: fill.initial, from: 'message', said: r.name ?? '', saidIn: 'photo'),
  );
});

/// "Add to the list" on a photo in a chat: read as Pay this account reads
/// it, and the account put on the person's list of people to pay, from
/// [from] (that message).
Future<void> collectFromChatPhoto(BuildContext context, Auth auth, String path, MessageFrom from) =>
    _fromChatPhoto(context, auth, path, (r, forms) => addReadingToList(context, auth, forms, r, photoFrom(from)));

/// A chat photo's message as the list keeps it, as the web keeps it too:
/// kind photo, its chat `<kind>:<id>`, so the same photo added on two
/// devices is one row.
MessageFrom photoFrom(MessageFrom m) => (kind: 'photo', chat: '${m.kind}:${m.chat}', message: m.message, from: m.from);

/// A chat's photo read on the phone (with AI where the phone found nothing
/// and the business allows it), its words read by Taskiem, and [use] given
/// the account found.
Future<void> _fromChatPhoto(BuildContext context, Auth auth, String path, Future<void> Function(ReadPaymentResponse r, PayForms forms) use) async {
  final photo = await readChatPhoto(context, auth, path);
  if (photo == null || !context.mounted) return;
  if (await _payFromPhotoWords(context, auth, photo.words, use) || !context.mounted) return;
  final words = await PhotoAI(auth).offer(
    context,
    photo,
    title: photo.words.isEmpty ? 'This phone read no words in it' : 'This phone found no account number',
    said: photo.words.isEmpty ? noWordsInPhoto : noAccountInPhoto,
  );
  if (words == null || !context.mounted) return;
  if (!await _payFromPhotoWords(context, auth, words, use) && context.mounted) showTkToast(context, noAccountInPhoto);
}

/// A photo's words read by Taskiem and [use] given what they hold: false
/// when they hold no account (nothing said), true when done or said.
Future<bool> _payFromPhotoWords(BuildContext context, Auth auth, String words, Future<void> Function(ReadPaymentResponse r, PayForms forms) use) async {
  if (words.isEmpty) return false;
  final ReadPaymentResponse r;
  final PayForms forms;
  try {
    r = await auth.api.readPayment(
      body: ReadPaymentRequest(text: words, from: 'photo'),
    );
    forms = await payFormsOf(auth);
  } on Object catch (e) {
    if (context.mounted) showTkToast(context, sayFailure(e), error: true);
    return true;
  }
  if (!context.mounted) return true;
  if (!r.found) return false;
  if (forms.account == null && forms.person == null) {
    showTkToast(context, 'Paying is not set up for this business yet.');
    return true;
  }
  await use(r, forms);
  return true;
}

/// The chip under a message that holds an account number: nothing until
/// Taskiem has read one in it.
class PayChip extends StatefulWidget {
  const PayChip({super.key, required this.auth, required this.id, required this.text, this.mine = false, this.from});
  final Auth auth;

  /// The message as the list of people to pay keeps it: with it, "Add to
  /// the list" beside Pay.
  final MessageFrom? from;

  /// The message's id and words.
  final String id;
  final String text;

  /// Sent by us: the chip on our side, as the bubble is.
  final bool mine;

  @override
  State<PayChip> createState() => _PayChipState();
}

class _PayChipState extends State<PayChip> {
  ReadPaymentResponse? _r;
  ChatAction? _account;
  ChatAction? _person;
  bool _opening = false;
  bool _savedHere = false;

  @override
  void initState() {
    super.initState();
    unawaited(_load());
  }

  Future<void> _load() async {
    final auth = widget.auth;
    if (!mayPayFromMessages(auth) || !mayHoldAccount(widget.text)) return;
    final (:account, :person) = await payFormsOf(auth);
    // No Pay in the catalogue: no payments account, or not theirs to use.
    if (account == null && person == null) return;
    final r = await _read(auth, widget.id, widget.text);
    if (!mounted || r == null) return;
    setState(() => (_r = r, _account = account, _person = person));
  }

  /// Add to the list: the reading kept on the person's list of people to
  /// pay, paid by the same account Pay would use.
  Future<void> _collect() async {
    final r = _r;
    final from = widget.from;
    if (r == null || from == null || _opening) return;
    _opening = true;
    try {
      await addReadingToList(context, widget.auth, (account: _account, person: _person), r, from, byName: _savedHere);
    } finally {
      _opening = false;
    }
  }

  Future<void> _open() async {
    final r = _r;
    if (r == null || _opening) return;
    final fill = payFromReading((account: _account, person: _person), r, byName: _savedHere);
    if (fill == null) {
      showTkToast(context, askToSave);
      return;
    }
    final (:action, :initial) = fill;
    _opening = true;
    final paid = await showTkSheet<Payment>(context, (_) => PaySheet(auth: widget.auth, action: action, initial: initial, from: 'message', said: r.name ?? ''));
    _opening = false;
    if (paid == null || !mounted) return;
    // Read again: the chip says it was paid.
    _reads[widget.auth]?.remove(widget.id);
    final again = await _read(widget.auth, widget.id, widget.text);
    if (mounted && again != null) setState(() => _r = again);
  }

  /// Save as…: the account kept in the business's saved things, and the
  /// chip says its @name from then on (the reading kept with it).
  Future<void> _save() async {
    final r = _r;
    if (r == null || _opening) return;
    _opening = true;
    final saved = await showTkSheet<PaySaved>(context, (_) => PaySaveSheet(auth: widget.auth, reading: r));
    _opening = false;
    if (saved == null || !mounted) return;
    final now = ReadPaymentResponse(
      found: r.found,
      accountNumber: r.accountNumber,
      amountKobo: r.amountKobo,
      bankCode: r.bankCode,
      bankName: r.bankName,
      likely: r.likely,
      name: r.name,
      paid: r.paid,
      payee: saved.name,
    );
    (_reads[widget.auth] ??= {})[widget.id] = Future.value(now);
    setState(() => (_r = now, _savedHere = true));
    showTkToast(context, 'Saved as @${saved.name}. ${saved.words}');
  }

  @override
  Widget build(BuildContext context) {
    final r = _r;
    if (r == null) return const SizedBox.shrink();
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final paid = r.paid;
    final staffAsk = _account == null && (r.payee ?? '').trim().isEmpty;
    final payee = (r.payee ?? '').trim();
    final who = payee.isEmpty ? 'this account' : '@$payee';
    final words = paid != null ? paidWords(paid) : 'Pay $who';
    final more = paid != null ? 'Pay again' : null;
    final semantics = [
      if (paid != null) '${paidWords(paid)}.',
      paid != null ? 'Pay $who again' : 'Pay $who',
      if ((r.accountNumber ?? '').length == 10) 'ending ${r.accountNumber!.substring(6)}',
    ].join(' ');
    final chip = Container(
      padding: const EdgeInsets.fromLTRB(5, 5, 14, 5),
      decoration: BoxDecoration(
        color: t.chatIn,
        borderRadius: BorderRadius.circular(999),
        boxShadow: [BoxShadow(color: const Color(0xFF000000).withValues(alpha: 0.08), blurRadius: 2, offset: const Offset(0, 1))],
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Container(
            width: 26,
            height: 26,
            alignment: Alignment.center,
            decoration: BoxDecoration(color: paid != null ? look.lime : look.teal, shape: BoxShape.circle),
            child: TkIcon(paid != null ? 'b-check' : 'b-bank', size: 15, stroke: 2.3, color: const Color(0xFFFFFFFF)),
          ),
          const SizedBox(width: 8),
          Flexible(
            child: Text.rich(
              TextSpan(
                children: [
                  TextSpan(
                    text: words,
                    style: tkText(size: 14, weight: paid != null ? 500 : 650, lineHeight: 1.3, color: t.text),
                  ),
                  if (more != null) ...[
                    TextSpan(
                      text: ' · ',
                      style: tkText(size: 14, lineHeight: 1.3, color: t.muted),
                    ),
                    TextSpan(
                      text: more,
                      style: tkText(size: 14, weight: 650, lineHeight: 1.3, color: t.accent),
                    ),
                  ],
                ],
              ),
              maxLines: 2,
              overflow: TextOverflow.ellipsis,
            ),
          ),
        ],
      ),
    );
    final pay = TapArea(
      child: Semantics(
        button: true,
        label: staffAsk ? '$semantics. Ask an owner to save it first' : semantics,
        excludeSemantics: true,
        onTap: () => unawaited(_open()),
        child: TkPress(
          onTap: () => unawaited(_open()),
          child: ConstrainedBox(
            constraints: const BoxConstraints(minHeight: 48),
            child: Center(widthFactor: 1, child: chip),
          ),
        ),
      ),
    );
    // Add to the list, where Pay could pay it (not for staff with an
    // account not saved yet), and not once paid.
    final collect = widget.from != null && !staffAsk && paid == null
        ? TapArea(
            child: Semantics(
              button: true,
              label: 'Add $who to your list of people to pay',
              excludeSemantics: true,
              onTap: () => unawaited(_collect()),
              child: TkPress(
                onTap: () => unawaited(_collect()),
                child: ConstrainedBox(
                  constraints: const BoxConstraints(minHeight: 48),
                  child: Center(
                    widthFactor: 1,
                    child: Container(
                      padding: const EdgeInsets.fromLTRB(10, 5, 14, 5),
                      decoration: BoxDecoration(
                        color: t.chatIn,
                        borderRadius: BorderRadius.circular(999),
                        boxShadow: [BoxShadow(color: const Color(0xFF000000).withValues(alpha: 0.08), blurRadius: 2, offset: const Offset(0, 1))],
                      ),
                      child: Row(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          SizedBox(
                            height: 26,
                            child: Center(child: TkIcon('b-people', size: 15, stroke: 2.3, color: t.accent)),
                          ),
                          const SizedBox(width: 6),
                          Text('Add to the list', style: tkText(size: 14, weight: 650, lineHeight: 1.3, color: t.accent)),
                        ],
                      ),
                    ),
                  ),
                ),
              ),
            ),
          )
        : null;
    final save = maySaveFromMessages(widget.auth, r)
        ? TapArea(
            child: Semantics(
              button: true,
              label: 'Save as… this account, to pay it by a name',
              excludeSemantics: true,
              onTap: () => unawaited(_save()),
              child: TkPress(
                onTap: () => unawaited(_save()),
                child: ConstrainedBox(
                  constraints: const BoxConstraints(minHeight: 48),
                  child: Center(
                    widthFactor: 1,
                    child: Container(
                      padding: const EdgeInsets.fromLTRB(10, 5, 14, 5),
                      decoration: BoxDecoration(
                        color: t.chatIn,
                        borderRadius: BorderRadius.circular(999),
                        boxShadow: [BoxShadow(color: const Color(0xFF000000).withValues(alpha: 0.08), blurRadius: 2, offset: const Offset(0, 1))],
                      ),
                      child: Row(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          SizedBox(
                            height: 26,
                            child: Center(child: TkIcon('b-plus', size: 15, stroke: 2.3, color: t.accent)),
                          ),
                          const SizedBox(width: 6),
                          Text('Save as…', style: tkText(size: 14, weight: 650, lineHeight: 1.3, color: t.accent)),
                        ],
                      ),
                    ),
                  ),
                ),
              ),
            ),
          )
        : null;
    return Align(
      alignment: widget.mine ? Alignment.centerRight : Alignment.centerLeft,
      child: Padding(
        padding: const EdgeInsets.only(top: 2, bottom: 4),
        child: save == null && collect == null
            ? pay
            : Wrap(spacing: 6, alignment: widget.mine ? WrapAlignment.end : WrapAlignment.start, children: [pay, ?collect, ?save]),
      ),
    );
  }
}
