// The chat tools' forms' parts (web/src/pages/ChatToolForms.tsx Stepper,
// Money, Switch, CategoryField; FormFrame.tsx; kit.css `.tk-form*`,
// `.tk-stepper`, `.tk-money`, `.tk-switch`): the scrolling body and its
// Save at the foot, − n +, a price in naira, a switch with its words, and
// a category with the ones already used as chips.

import 'dart:async';

import 'package:flutter/material.dart' show InputDecoration, Material, MaterialType, TextField;
import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';

import '../api/transport.dart' show ApiError, Offline;
import 'kit.dart';
import 'sheet.dart';
import 'tap_area.dart';
import '../theme/theme.dart';

/// toolforms.ts nairaKobo: naira as typed ("78,000"), in kobo; null when
/// it is not money.
int? nairaKobo(String text) {
  final t = text.replaceAll(RegExp(r'[₦,\s]'), '');
  if (!RegExp(r'^\d+(\.\d{1,2})?$').hasMatch(t)) return null;
  return (double.parse(t) * 100).round();
}

/// toolforms.ts nairaText: kobo as typed naira ("78,000", "1,250.50").
String nairaText(int kobo) {
  final whole = (kobo ~/ 100).toString().replaceAllMapped(RegExp(r'\B(?=(\d{3})+(?!\d))'), (_) => ',');
  final frac = kobo % 100;
  return frac == 0 ? whole : '$whole.${frac.toString().padLeft(2, '0')}';
}

/// FormFrame.tsx Saver: saves, and answers its diary line (null: nothing
/// changed to tell).
typedef Saver = Future<String?> Function();

/// FormFrame.tsx: the form's body, scrolling, and its foot: Save (or
/// Cancel | Save changes), or, for someone who may only look, the words
/// that say so. A failed save says why over the button.
class TkToolForm extends StatefulWidget {
  const TkToolForm({
    super.key,
    required this.children,
    required this.save,
    required this.onSaved,
    this.saveWords = 'Save',
    this.ready = true,
    this.canEdit = true,
    this.onCancel,
  });
  final List<Widget> children;
  final Saver save;
  final void Function(String? line) onSaved;
  final String saveWords;
  final bool ready;
  final bool canEdit;
  final VoidCallback? onCancel;

  @override
  State<TkToolForm> createState() => _TkToolFormState();
}

class _TkToolFormState extends State<TkToolForm> {
  bool _busy = false;
  String? _error;

  Future<void> _submit() async {
    if (_busy || !widget.ready || !widget.canEdit) return;
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final line = await widget.save();
      widget.onSaved(line);
    } on ApiError catch (e) {
      if (mounted) setState(() => (_error = e.message, _busy = false));
    } on Offline {
      if (mounted) setState(() => (_error = 'You’re offline. Try again when you’re back online.', _busy = false));
    } on FormProblem catch (e) {
      if (mounted) setState(() => (_error = e.words, _busy = false));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final go = _busy || !widget.ready ? null : _submit;
    final words = _busy ? 'Saving…' : (widget.onCancel != null && widget.saveWords == 'Save' ? 'Save changes' : widget.saveWords);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Expanded(
          // `fieldset[disabled]`: shown, not to be changed.
          child: IgnorePointer(
            ignoring: !widget.canEdit,
            child: TkFormBody(children: [...widget.children, if (_error != null) TkErrorBox(_error!)]),
          ),
        ),
        TkSheetFoot(
          child: !widget.canEdit
              ? Text(
                  'You can see this. Changing it needs someone who may edit it.',
                  textAlign: TextAlign.center,
                  style: tkText(size: 14, color: t.muted),
                )
              : widget.onCancel != null
              // `.tk-form-foot-two`: Cancel as wide as its words, Save the rest.
              ? Row(
                  children: [
                    TkFormButton(label: 'Cancel', onTap: widget.onCancel),
                    const SizedBox(width: 12),
                    Expanded(child: TkCta(label: words, onTap: go)),
                  ],
                )
              : TkCta(label: words, onTap: go),
        ),
      ],
    );
  }
}

/// A save that cannot go, said in the person's words (a check before
/// sending).
class FormProblem implements Exception {
  FormProblem(this.words);
  final String words;
}

/// `.tk-form-foot-two .tk-btn.soft`: 56 high, 22 each side, 16 px.
class TkFormButton extends StatelessWidget {
  const TkFormButton({super.key, required this.label, this.onTap});
  final String label;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    return Semantics(
      button: true,
      enabled: onTap != null,
      label: label,
      excludeSemantics: true,
      child: TkPress(
        onTap: onTap,
        child: Container(
          constraints: const BoxConstraints(minHeight: 56),
          padding: const EdgeInsets.symmetric(horizontal: 22),
          alignment: Alignment.center,
          decoration: BoxDecoration(color: t.tkSoft, borderRadius: BorderRadius.circular(999)),
          child: Text(label, style: tkText(size: 16, weight: 700, color: dark ? t.text : t.brand)),
        ),
      ),
    );
  }
}

/// `.tk-form-row`: words on the left (a title, a line under it), a
/// control on the right.
class TkFormRow extends StatelessWidget {
  const TkFormRow({super.key, required this.title, this.sub, required this.trailing});
  final String title;
  final String? sub;
  final Widget trailing;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Row(
      children: [
        Expanded(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(title, style: tkText(size: 15, weight: 600, color: t.text)),
              if (sub != null) Text(sub!, style: tkText(size: 13, color: t.muted)),
            ],
          ),
        ),
        const SizedBox(width: 12),
        trailing,
      ],
    );
  }
}

/// ChatToolForms.tsx Switch: a form row whose control is the switch.
class TkSwitchRow extends StatelessWidget {
  const TkSwitchRow({super.key, required this.label, this.hint, required this.on, this.onChanged});
  final String label;
  final String? hint;
  final bool on;
  final ValueChanged<bool>? onChanged;

  @override
  Widget build(BuildContext context) => TkFormRow(
    title: label,
    sub: hint,
    trailing: TkSwitch(label: label, on: on, onChanged: onChanged),
  );
}

/// ChatToolForms.tsx Stepper (`.tk-stepper`): − n + in an outlined pill,
/// the number typed too; never under nought.
class TkStepper extends StatefulWidget {
  const TkStepper({super.key, required this.label, required this.value, required this.onChanged});
  final String label;
  final int value;
  final ValueChanged<int> onChanged;

  @override
  State<TkStepper> createState() => _TkStepperState();
}

class _TkStepperState extends State<TkStepper> {
  late final _text = TextEditingController(text: '${widget.value}');
  final _focus = FocusNode();

  @override
  void initState() {
    super.initState();
    _focus.addListener(() => setState(() {}));
  }

  @override
  void didUpdateWidget(TkStepper oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (widget.value != (int.tryParse(_text.text) ?? 0) || (_text.text.isEmpty && !_focus.hasFocus)) _text.text = '${widget.value}';
  }

  @override
  void dispose() {
    _text.dispose();
    _focus.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    Widget step(String sign, String label, int to) => TapArea(
      child: Semantics(
        button: true,
        label: label,
        excludeSemantics: true,
        child: GestureDetector(
          behavior: HitTestBehavior.opaque,
          onTap: () => widget.onChanged(to < 0 ? 0 : to),
          child: SizedBox(width: 44, height: 44, child: Center(child: Text(sign, style: tkText(size: 20, color: t.text)))),
        ),
      ),
    );
    return Container(
      decoration: BoxDecoration(
        border: Border.all(color: t.border, width: 1.5),
        borderRadius: BorderRadius.circular(999),
        boxShadow: _focus.hasFocus ? t.ring : const [],
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          step('−', 'One fewer', widget.value - 1),
          SizedBox(
            width: 52,
            child: Semantics(
              label: widget.label,
              textField: true,
              child: EditableText(
                controller: _text,
                focusNode: _focus,
                textAlign: TextAlign.center,
                keyboardType: TextInputType.number,
                inputFormatters: [FilteringTextInputFormatter.digitsOnly, LengthLimitingTextInputFormatter(7)],
                style: tkText(size: 16, weight: 700, color: t.text),
                cursorColor: t.text,
                backgroundCursorColor: t.muted,
                showCursor: !TkField.debugHideCaret,
                onChanged: (v) => widget.onChanged(int.tryParse(v) ?? 0),
              ),
            ),
          ),
          step('+', 'One more', widget.value + 1),
        ],
      ),
    );
  }
}

/// ChatToolForms.tsx Money (`.tk-money`): ₦ and the amount, 20 px bold, in
/// an outlined box; the ring while typed in.
class TkMoneyField extends StatefulWidget {
  const TkMoneyField({super.key, required this.label, required this.controller, this.hint, this.error});
  final String label;
  final TextEditingController controller;
  final String? hint;
  final String? error;

  @override
  State<TkMoneyField> createState() => _TkMoneyFieldState();
}

class _TkMoneyFieldState extends State<TkMoneyField> {
  final _focus = FocusNode();

  @override
  void initState() {
    super.initState();
    _focus.addListener(() => setState(() {}));
  }

  @override
  void dispose() {
    _focus.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final style = tkText(size: 20, weight: 700, color: t.text);
    return TkKitField(
      label: widget.label,
      hint: widget.hint,
      error: widget.error,
      child: TapField(
        child: Container(
          constraints: const BoxConstraints(minHeight: 54),
          padding: const EdgeInsets.symmetric(horizontal: 16),
          decoration: BoxDecoration(
            color: t.panel,
            border: Border.all(color: _focus.hasFocus ? t.accent : t.border, width: 1.5),
            borderRadius: BorderRadius.circular(14),
            boxShadow: _focus.hasFocus ? t.ring : const [],
          ),
          child: Row(
            children: [
              ExcludeSemantics(child: Text('₦', style: style)),
              const SizedBox(width: 6),
              Expanded(
                child: Semantics(
                  label: '${widget.label} in naira',
                  child: Material(
                    type: MaterialType.transparency,
                    child: TextField(
                      controller: widget.controller,
                      focusNode: _focus,
                      keyboardType: const TextInputType.numberWithOptions(decimal: true),
                      cursorColor: t.text,
                      showCursor: !TkField.debugHideCaret,
                      style: style,
                      decoration: const InputDecoration.collapsed(hintText: null),
                    ),
                  ),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

/// kit.css `.tk-switch`: 52 by 32, the knob sliding; pine when on (lime
/// in the dark theme, and on a pine card).
class TkSwitch extends StatelessWidget {
  const TkSwitch({super.key, required this.label, required this.on, this.onChanged, this.onPine = false});
  final String label;
  final bool on;
  final ValueChanged<bool>? onChanged;

  /// On a pine ground (`.tk-p-found.on`): the lime track, the knob in pine's ink.
  final bool onPine;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    final still = MediaQuery.maybeDisableAnimationsOf(context) ?? false;
    final track = !on ? t.borderStrong : (onPine || dark ? t.brandDark : t.brand);
    final knob = on && onPine ? t.tkOnPine : const Color(0xFFFFFFFF);
    return TapArea(
      child: Semantics(
        toggled: on,
        label: label,
        enabled: onChanged != null,
        excludeSemantics: true,
        child: GestureDetector(
          onTap: onChanged == null ? null : () => onChanged!(!on),
          child: Opacity(
            opacity: onChanged == null ? 0.6 : 1,
            child: AnimatedContainer(
              duration: still ? Duration.zero : const Duration(milliseconds: 200),
              width: 52,
              height: 32,
              decoration: BoxDecoration(color: track, borderRadius: BorderRadius.circular(999)),
              child: AnimatedAlign(
                duration: still ? Duration.zero : const Duration(milliseconds: 250),
                curve: Curves.easeOutBack,
                alignment: on ? Alignment.centerRight : Alignment.centerLeft,
                child: Container(
                  width: 26,
                  height: 26,
                  margin: const EdgeInsets.all(3),
                  decoration: BoxDecoration(
                    color: knob,
                    shape: BoxShape.circle,
                    boxShadow: const [BoxShadow(color: Color(0x40000000), offset: Offset(0, 1), blurRadius: 3)],
                  ),
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }
}

