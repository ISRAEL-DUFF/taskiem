// Pay from a message (paying people, phase 2; docs/spec/chat-pay-people.md
// "Pay one person", "Three ways in"): a message in one of the business's
// chats that holds a 10-digit account number gets a "Pay this account"
// chip under it, for those who may pay (money.pay). The words are checked
// here first (ten digits together), then read by Taskiem (POST
// /v1/payments/read): the number, the bank, the amount and the name the
// message gives, the business's saved account with that number, and its
// last payment to it. Tapping opens the Pay sheet filled in: the bank and
// number for an owner, the saved account for staff (who pay saved accounts
// only). A chip already paid says so, and pays again.

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
import 'pay_sheet.dart' show PaySheet;
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

/// The chip under a message that holds an account number: nothing until
/// Taskiem has read one in it.
class PayChip extends StatefulWidget {
  const PayChip({super.key, required this.auth, required this.id, required this.text, this.mine = false});
  final Auth auth;

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

  @override
  void initState() {
    super.initState();
    unawaited(_load());
  }

  Future<void> _load() async {
    final auth = widget.auth;
    if (!mayPayFromMessages(auth) || !mayHoldAccount(widget.text)) return;
    final topics = await catalogueOf(auth).catchError((Object _) => <ChatTopic>[]);
    ChatAction? find(String id) => [for (final t in topics) ...t.actions].where((a) => a.id == id && a.confirm == 'passkey').firstOrNull;
    final account = find('pay.account'), person = find('pay.person');
    // No Pay in the catalogue: no payments account, or not theirs to use.
    if (account == null && person == null) return;
    final r = await _read(auth, widget.id, widget.text);
    if (!mounted || r == null) return;
    setState(() => (_r = r, _account = account, _person = person));
  }

  Future<void> _open() async {
    final r = _r;
    if (r == null || _opening) return;
    final amount = (r.amountKobo ?? 0) >= 100 ? _nairaTyped(r.amountKobo!) : '';
    // An owner pays the number and bank; staff the business's saved account
    // with that number.
    final payee = (r.payee ?? '').trim();
    final ChatAction action;
    final Map<String, String> initial;
    if (_account case final a?) {
      action = a;
      initial = {
        for (final f in a.fields ?? const <ChatField>[])
          if (f.kind == 'account')
            f.key: r.accountNumber ?? ''
          else if (f.kind == 'bank' && (r.bankName ?? '').isNotEmpty)
            f.key: r.bankName!
          else if (f.kind == 'money' && amount.isNotEmpty)
            f.key: amount,
      };
    } else if (_person != null && payee.isNotEmpty) {
      action = _person!;
      initial = {
        for (final f in action.fields ?? const <ChatField>[])
          if (f.kind == 'payee')
            f.key: '@$payee'
          else if (f.kind == 'money' && amount.isNotEmpty)
            f.key: amount,
      };
    } else {
      showTkToast(context, 'Ask an owner to save this account: then you can pay it.');
      return;
    }
    _opening = true;
    final paid = await showTkSheet<Payment>(
      context,
      (_) => PaySheet(auth: widget.auth, action: action, initial: initial, from: 'message', said: r.name ?? ''),
    );
    _opening = false;
    if (paid == null || !mounted) return;
    // Read again: the chip says it was paid.
    _reads[widget.auth]?.remove(widget.id);
    final again = await _read(widget.auth, widget.id, widget.text);
    if (mounted && again != null) setState(() => _r = again);
  }

  @override
  Widget build(BuildContext context) {
    final r = _r;
    if (r == null) return const SizedBox.shrink();
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final paid = r.paid;
    final staffAsk = _account == null && (r.payee ?? '').trim().isEmpty;
    final words = paid != null ? paidWords(paid) : 'Pay this account';
    final more = paid != null ? 'Pay again' : null;
    final semantics = [
      if (paid != null) '${paidWords(paid)}.',
      paid != null ? 'Pay this account again' : 'Pay this account',
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
                    TextSpan(text: ' · ', style: tkText(size: 14, lineHeight: 1.3, color: t.muted)),
                    TextSpan(text: more, style: tkText(size: 14, weight: 650, lineHeight: 1.3, color: t.accent)),
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
    return Align(
      alignment: widget.mine ? Alignment.centerRight : Alignment.centerLeft,
      child: Padding(
        padding: const EdgeInsets.only(top: 2, bottom: 4),
        child: TapArea(
          child: Semantics(
            button: true,
            label: staffAsk ? '$semantics. Ask an owner to save it first' : semantics,
            excludeSemantics: true,
            onTap: () => unawaited(_open()),
            child: TkPress(
              onTap: () => unawaited(_open()),
              child: ConstrainedBox(constraints: const BoxConstraints(minHeight: 48), child: Center(widthFactor: 1, child: chip)),
            ),
          ),
        ),
      ),
    );
  }
}
