// Save as… beside "Pay this account" (paying people; docs/spec/
// chat-pay-people.md "Save as a beneficiary"): an account read from a
// message, not saved yet, kept in the business's saved things with a name
// to call it up by ("@chidi-rider"), so it is paid by @name from then on.
// For those who may manage the business's saved things (saved.manage). The
// name is made from the message's name as the server makes one; the bank
// is the message's, else picked as the Pay sheet picks it (the banks the
// number can be at first); the number is the message's, shown masked.
// Saved with POST /v1/saved, as Saved's own sheet saves an account.

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart' show ApiError;
import '../app/auth.dart';
import '../kit/look.dart';
import '../kit/mobile.dart';
import '../kit/sheet.dart';
import '../theme/look.dart';
import 'approve.dart' show TkTextBox;
import 'my_chat.dart' show sayFailure;
import 'pay_sheet.dart' show pickPayBank;
import 'save_after.dart' show savedNameFor, savedNow;
import 'saved_accounts.dart' show SavedApi, cleanSavedName, savedCheck, savedNameProblem;

/// Whether this person is offered Save as… on a reading: the account is
/// not saved yet and they may manage the business's saved things.
bool maySaveFromMessages(Auth auth, ReadPaymentResponse r) =>
    auth.me?.personal != true && auth.can('saved.manage') && (r.payee ?? '').trim().isEmpty && (r.accountNumber ?? '').length == 10;

/// "••••••6789": the number as the sheet shows it.
String maskedAccount(String number) => number.length < 4 ? number : '••••••${number.substring(number.length - 4)}';

/// What saving said, in plain words: the name, and the bank's name for the
/// account or why it is not checked.
typedef PaySaved = ({String name, String words});

/// The sheet: pops what was saved, or nothing when put away.
class PaySaveSheet extends StatefulWidget {
  const PaySaveSheet({super.key, required this.auth, required this.reading});
  final Auth auth;
  final ReadPaymentResponse reading;

  @override
  State<PaySaveSheet> createState() => _PaySaveSheetState();
}

class _PaySaveSheetState extends State<PaySaveSheet> {
  late final ReadPaymentResponse _r = widget.reading;
  late final _name = TextEditingController(text: savedNameFor(_r.name ?? '').isEmpty ? '' : '@${savedNameFor(_r.name!)}');
  late final _title = TextEditingController(text: (_r.name ?? '').trim());
  late final _number = TextEditingController(text: maskedAccount(_r.accountNumber ?? ''));
  late final SavedApi _api = SavedApi(widget.auth, false);
  List<ListPayBanksResponseBanksItem>? _banks;
  String _from = '';
  // The bank: the message's until another is picked.
  late String _bankName = (_r.bankName ?? '').trim();
  late String _bankCode = (_r.bankCode ?? '').trim();
  bool _loading = false;
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    unawaited(_loadBanks());
  }

  @override
  void dispose() {
    for (final c in [_name, _title, _number]) {
      c.dispose();
    }
    super.dispose();
  }

  /// The banks this number can be at first, from the payments account.
  Future<void> _loadBanks() async {
    if (_banks != null || _loading) return;
    setState(() => _loading = true);
    try {
      final r = await widget.auth.api.listPayBanks(account: _r.accountNumber);
      if (mounted) setState(() => (_banks = r.banks, _from = r.from));
    } on Object catch (e) {
      // The message's bank still saves by name; picking another waits.
      if (mounted && _bankName.isEmpty) setState(() => _error = sayFailure(e));
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  Future<void> _pickBank() async {
    if (_banks == null) await _loadBanks();
    final list = _banks;
    if (list == null || !mounted) return;
    final v = await pickPayBank(context, list, value: _bankName.isEmpty ? null : _bankName);
    if (v == null || !mounted) return;
    final b = list.firstWhere((b) => b.name == v);
    setState(() => (_bankName = b.name, _bankCode = b.code, _error = null));
  }

  /// The bank's code as the list it came from has it, with that list: sent
  /// only when it is the payments account's own (else by name).
  ({String? code, String? from}) get _code {
    final list = _banks;
    if (list == null || _from.isEmpty || _bankCode.isEmpty) return (code: null, from: null);
    final inList = list.any((b) => b.code == _bankCode);
    return inList ? (code: _bankCode, from: _from) : (code: null, from: null);
  }

  Future<void> _save() async {
    final problem =
        savedNameProblem(_name.text) ??
        (_title.text.trim().isEmpty ? 'Give it a title, like Chidi, rider.' : null) ??
        (_bankName.isEmpty ? 'Choose the bank.' : null);
    if (problem != null) return setState(() => _error = problem);
    setState(() => (_busy = true, _error = null));
    final code = _code;
    try {
      final r = await _api.add(
        SavedObjectIn(
          kind: 'bank_account',
          name: cleanSavedName(_name.text),
          title: _title.text.trim(),
          bankName: _bankName,
          bankCode: code.code,
          codeFrom: code.from,
          accountNumber: _r.accountNumber,
        ),
      );
      savedNow();
      final check = savedCheck(r.saved);
      final why = (r.checkProblem ?? '').trim();
      final words = check.checked ? check.words : (why.isNotEmpty ? 'Name not checked yet: $why' : check.words);
      if (mounted) Navigator.of(context).pop<PaySaved>((name: r.saved.name, words: words));
    } on ApiError catch (e) {
      // The server's own words (the bank has no such account, past the
      // limit), the name taken said as Saved says it.
      final words = e.has('name_taken') ? 'That name is taken: choose another.' : sayFailure(e);
      if (mounted) setState(() => (_busy = false, _error = words));
    } on Object catch (e) {
      if (mounted) setState(() => (_busy = false, _error = sayFailure(e)));
    }
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final shown = _bankName.isNotEmpty ? _bankName : (_loading ? 'Loading the banks…' : 'Choose the bank');
    return TkFormSheet(
      title: 'Save this account',
      crumb: 'Pay this account',
      action: _busy ? 'Saving…' : 'Save account',
      onAction: _busy ? null : _save,
      children: [
        TkFormGroup(
          children: [
            TkKitField(label: 'Name to call them by', hint: 'Such as @chidi-rider: then Pay @chidi-rider', child: TkTextBox(controller: _name, max: 33, plain: true)),
            TkKitField(label: 'Title', hint: 'How it is shown, such as Chidi, rider', child: TkTextBox(controller: _title, max: 80)),
          ],
        ),
        TkFormGroup(
          children: [
            TkKitField(label: 'Bank', field: false, child: TkPickBox(label: 'Bank', shown: shown, onTap: () => unawaited(_pickBank()))),
            TkKitField(label: 'Account number', hint: 'As the message gives it', child: TkTextBox(controller: _number, enabled: false)),
          ],
        ),
        if (_error != null) TkCallout(leading: TkSquare('b-warn', color: look.red), title: _error!, color: look.red, subLines: 3),
      ],
    );
  }
}
