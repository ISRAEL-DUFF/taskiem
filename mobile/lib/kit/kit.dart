// The web's parts, drawn the same (web/src/styles.css: button, input,
// label.field, .hint, a). Never Material's: no ripple, no Material shadows.

import 'package:flutter/material.dart' show TextField, InputDecoration, Material, MaterialType;
import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';

import '../theme/theme.dart';
import 'tap_area.dart';

/// A press, as the web's `button:active { transform: scale(0.98) }` with its
/// 80 ms transition: the thing gives a little while held.
class TkPress extends StatefulWidget {
  const TkPress({super.key, required this.child, this.onTap, this.scale = 0.98});

  final Widget child;
  final VoidCallback? onTap;
  final double scale;

  @override
  State<TkPress> createState() => _TkPressState();
}

class _TkPressState extends State<TkPress> {
  bool _down = false;

  void _set(bool v) {
    if (widget.onTap == null || _down == v) return;
    setState(() => _down = v);
  }

  @override
  Widget build(BuildContext context) {
    final still = MediaQuery.maybeDisableAnimationsOf(context) ?? false;
    return GestureDetector(
      behavior: HitTestBehavior.opaque,
      onTapDown: (_) => _set(true),
      onTapUp: (_) => _set(false),
      onTapCancel: () => _set(false),
      onTap: widget.onTap,
      child: AnimatedScale(
        scale: _down && !still ? widget.scale : 1,
        duration: still ? Duration.zero : const Duration(milliseconds: 80),
        curve: Curves.ease,
        child: widget.child,
      ),
    );
  }
}

/// The web's `button`: pine (lime on dark) when [primary], else the panel
/// with a strong border. [minHeight] is 34 on the web, 44 on its sign-in card.
class TkButton extends StatelessWidget {
  const TkButton({
    super.key,
    required this.label,
    this.onPressed,
    this.primary = false,
    this.minHeight = 34,
    this.enabled = true,
  });

  final String label;
  final VoidCallback? onPressed;
  final bool primary;
  final double minHeight;

  /// `button:disabled`: faded to 45%, and nothing happens on a press.
  final bool enabled;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final fg = primary ? t.primaryText : t.text;
    final box = Container(
      constraints: BoxConstraints(minHeight: minHeight),
      padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 6),
      alignment: Alignment.center,
      decoration: BoxDecoration(
        color: primary ? t.primary : t.panel,
        border: Border.all(color: primary ? t.primary : t.borderStrong),
        borderRadius: BorderRadius.circular(Tk.radiusSm),
        // `box-shadow: 0 1px 2px rgba(14, 15, 12, 0.12)` on primary; the
        // theme's --shadow on the rest (none in dark).
        boxShadow: primary
            ? const [BoxShadow(offset: Offset(0, 1), blurRadius: 0.87, color: Color(0x1F0E0F0C))]
            : t.shadow,
      ),
      child: Text(
        label,
        textAlign: TextAlign.center,
        style: tkText(size: 13.5, weight: 500, lineHeight: 1.2, color: fg),
      ),
    );
    return TapArea(
      child: Semantics(
        button: true,
        enabled: enabled,
        child: Opacity(
          opacity: enabled ? 1 : 0.45,
          child: TkPress(onTap: enabled ? (onPressed ?? () {}) : null, child: box),
        ),
      ),
    );
  }
}

/// The web's `label.field`: its words above a field without an outline, a
/// soft fill that deepens on focus with the ring (styles.css, "Fields
/// without outlines").
class TkField extends StatefulWidget {
  /// Tests that compare screens hide the blinking caret, as the browser's
  /// screenshots do.
  static bool debugHideCaret = false;

  const TkField({
    super.key,
    required this.label,
    this.controller,
    this.autofocus = false,
    this.obscure = false,
    this.keyboard,
    this.autofill,
    this.minHeight = 36,
    this.onSubmitted,
    this.onChanged,
    this.digits,
  });

  final String label;
  final TextEditingController? controller;
  final bool autofocus;
  final bool obscure;
  final TextInputType? keyboard;
  final Iterable<String>? autofill;
  final double minHeight;
  final ValueChanged<String>? onSubmitted;
  final ValueChanged<String>? onChanged;

  /// Only digits, at most this many (a code).
  final int? digits;

  @override
  State<TkField> createState() => _TkFieldState();
}

class _TkFieldState extends State<TkField> {
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
    final focused = _focus.hasFocus;
    final still = MediaQuery.maybeDisableAnimationsOf(context) ?? false;
    // The words and the field are one to a screen reader and a finger.
    return TapField(child: Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Padding(
          padding: const EdgeInsets.only(bottom: 6),
          child: Text(widget.label, style: tkText(size: 13, weight: 500, color: t.text)),
        ),
        // `transition: background-color 0.18s ease, box-shadow 0.18s ease`
        AnimatedContainer(
          duration: still ? Duration.zero : const Duration(milliseconds: 180),
          curve: Curves.ease,
          constraints: BoxConstraints(minHeight: widget.minHeight),
          padding: const EdgeInsets.symmetric(horizontal: 12),
          alignment: Alignment.centerLeft,
          decoration: BoxDecoration(
            color: focused ? t.fieldFocus : t.field,
            borderRadius: BorderRadius.circular(Tk.radiusSm),
            boxShadow: focused ? t.ring : const [],
          ),
          child: Material(
            type: MaterialType.transparency,
            child: TextField(
              controller: widget.controller,
              focusNode: _focus,
              autofocus: widget.autofocus,
              obscureText: widget.obscure,
              keyboardType: widget.keyboard,
              autofillHints: widget.autofill,
              onSubmitted: widget.onSubmitted,
              onChanged: widget.onChanged,
              cursorColor: t.text,
              showCursor: !TkField.debugHideCaret,
              style: tkText(size: 14, color: t.text),
              decoration: const InputDecoration.collapsed(hintText: null),
              inputFormatters: widget.digits == null
                  ? const <TextInputFormatter>[]
                  : [FilteringTextInputFormatter.digitsOnly, LengthLimitingTextInputFormatter(widget.digits)],
            ),
          ),
        ),
      ],
    ));
  }
}

/// The web's `.hint`: muted, 13 px.
TextStyle tkHint(TkTokens t) => tkText(size: 13, color: t.muted);

/// A link in words (`a`, underlined on the sign-in card), with the web's
/// 4 px above and below so it is easy to press.
class TkLink extends StatelessWidget {
  const TkLink({super.key, required this.label, this.onTap});

  final String label;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return TapArea(
      child: Semantics(
        link: true,
        enabled: onTap != null,
        onTap: onTap,
        child: GestureDetector(
          behavior: HitTestBehavior.opaque,
          onTap: onTap,
          child: Padding(
            padding: const EdgeInsets.symmetric(vertical: 4),
            child: Text(label, style: tkText(size: 13, color: t.accent, decoration: TextDecoration.underline)),
          ),
        ),
      ),
    );
  }
}

/// The web's `ErrorBox` (`.error`): the server's words in the danger colour
/// on its faint tint, a thin border, 12 px above and below.
class TkErrorBox extends StatelessWidget {
  const TkErrorBox(this.message, {super.key});

  final String message;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 12),
      child: Semantics(
        liveRegion: true,
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 11),
          decoration: BoxDecoration(
            color: cssMix(t.danger, t.panel, 0.06),
            border: Border.all(color: cssMix(t.danger, const Color(0x00000000), 0.25)),
            borderRadius: BorderRadius.circular(10),
          ),
          child: Text(message, style: tkText(size: 14, color: cssMix(t.danger, t.text, 0.82))),
        ),
      ),
    );
  }
}

/// The web's `button.link`: a button that reads as a link (13.5 px,
/// weight 500, the accent colour, underlined), 40 px tall to press, with the
/// button's faint shadow and nothing else.
class TkLinkButton extends StatelessWidget {
  const TkLinkButton({super.key, required this.label, this.onPressed});

  final String label;
  final VoidCallback? onPressed;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return TapArea(child: Semantics(
      button: true,
      child: TkPress(
        onTap: onPressed ?? () {},
        child: Container(
          constraints: const BoxConstraints(minHeight: 40),
          padding: const EdgeInsets.symmetric(horizontal: 4),
          child: Align(
            widthFactor: 1,
            heightFactor: 1,
            child: Text(label, style: tkText(size: 14, weight: 600, lineHeight: 1.2, color: TkTheme.of(context).mode == TkMode.dark ? t.brandDark : t.brand)),
          ),
        ),
      ),
    ));
  }
}

/// A choice among several, as the web shows one: Chrome's own radio, tinted
/// with `accent-color: var(--primary)` (styles.css). Chrome adjusts the
/// accent for its controls (a darker pine's dot in light, a lighter lime in
/// dark), so these colours are measured from its drawing, 16 px across with
/// a 1 px ring and an 8 px dot; the parity test keeps them true.
class TkRadio extends StatelessWidget {
  const TkRadio({super.key, required this.checked});
  final bool checked;

  @override
  Widget build(BuildContext context) {
    final dark = TkTheme.of(context).mode == TkMode.dark;
    return SizedBox(width: 16, height: 16, child: CustomPaint(painter: _RadioPainter(checked: checked, dark: dark)));
  }
}

class _RadioPainter extends CustomPainter {
  _RadioPainter({required this.checked, required this.dark});
  final bool checked;
  final bool dark;

  @override
  void paint(Canvas canvas, Size size) {
    final c = size.center(Offset.zero);
    final fill = dark ? const Color(0xFF3B3B3B) : const Color(0xFFFFFFFF);
    final ring = checked
        ? (dark ? const Color(0xFFD8F29A) : const Color(0xFF0B1512))
        : (dark ? const Color(0xFF858585) : const Color(0xFF767676));
    canvas.drawCircle(c, 7.5, Paint()..color = fill);
    canvas.drawCircle(c, 7.5, Paint()
      ..color = ring
      ..style = PaintingStyle.stroke
      ..strokeWidth = 1);
    if (checked) canvas.drawCircle(c, 4, Paint()..color = dark ? const Color(0xFFDBF69C) : const Color(0xFF030D0A));
  }

  @override
  bool shouldRepaint(_RadioPainter old) => old.checked != checked || old.dark != dark;
}

/// A yes-or-no, as Chrome draws a checkbox with the web's accent: 16 px, a
/// 1 px border and 2 px corners, filled with the accent and ticked when on.
class TkCheckbox extends StatelessWidget {
  const TkCheckbox({super.key, required this.checked});
  final bool checked;

  @override
  Widget build(BuildContext context) {
    final dark = TkTheme.of(context).mode == TkMode.dark;
    return SizedBox(width: 16, height: 16, child: CustomPaint(painter: _CheckPainter(checked: checked, dark: dark)));
  }
}

class _CheckPainter extends CustomPainter {
  _CheckPainter({required this.checked, required this.dark});
  final bool checked;
  final bool dark;

  @override
  void paint(Canvas canvas, Size size) {
    final r = RRect.fromRectAndRadius(const Rect.fromLTWH(0.5, 0.5, 15, 15), const Radius.circular(2));
    final accent = dark ? const Color(0xFFDBF69C) : const Color(0xFF030D0A);
    if (checked) {
      canvas.drawRRect(r, Paint()..color = accent);
      final tick = Path()
        ..moveTo(3.5, 8)
        ..lineTo(6.5, 11)
        ..lineTo(12.5, 5);
      canvas.drawPath(tick, Paint()
        ..color = dark ? const Color(0xFF151614) : const Color(0xFFFFFFFF)
        ..style = PaintingStyle.stroke
        ..strokeWidth = 2
        ..strokeCap = StrokeCap.round
        ..strokeJoin = StrokeJoin.round);
      return;
    }
    canvas.drawRRect(r, Paint()..color = dark ? const Color(0xFF3B3B3B) : const Color(0xFFFFFFFF));
    canvas.drawRRect(r, Paint()
      ..color = dark ? const Color(0xFF858585) : const Color(0xFF767676)
      ..style = PaintingStyle.stroke
      ..strokeWidth = 1);
  }

  @override
  bool shouldRepaint(_CheckPainter old) => old.checked != checked || old.dark != dark;
}

/// The web's `label.row` with a radio or checkbox in it: the box (with
/// Chrome's margins: 3 px above and right, 5 px left), 8 px, the words;
/// 16 px below. The whole row is pressed.
class TkChoiceRow extends StatelessWidget {
  const TkChoiceRow({super.key, required this.control, required this.label, required this.onTap, this.radio = true, this.checked});
  final Widget control;
  final Widget label;

  /// Null: shown, not to be changed (`disabled`), and said so.
  final VoidCallback? onTap;
  final bool radio;

  /// Whether it is the one chosen, said to a screen reader; by default
  /// what [control] (a [TkRadio] or [TkCheckbox]) shows.
  final bool? checked;

  @override
  Widget build(BuildContext context) {
    final c = control;
    final on = checked ?? (c is TkRadio ? c.checked : (c is TkCheckbox ? c.checked : null));
    return Padding(
      padding: const EdgeInsets.only(bottom: 16),
      child: TapArea(
        child: Semantics(
          inMutuallyExclusiveGroup: radio,
          checked: on,
          enabled: onTap != null,
          child: GestureDetector(
            behavior: HitTestBehavior.opaque,
            onTap: onTap,
            child: Row(
              children: [
                Padding(padding: const EdgeInsets.fromLTRB(5, 3, 3, 0), child: control),
                const SizedBox(width: 8),
                Expanded(child: label),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

/// The web's `.badge`: a pill 22 px tall, its tone's dot, the words in the
/// tone mixed with the text, on the tone's faint tint.
class TkBadge extends StatelessWidget {
  const TkBadge(this.label, {super.key, required this.tone});
  final String label;
  final Color tone;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Container(
      height: 22,
      padding: const EdgeInsets.symmetric(horizontal: 9),
      decoration: BoxDecoration(color: cssMix(tone, t.panel, 0.11), borderRadius: BorderRadius.circular(999)),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Container(width: 6, height: 6, decoration: BoxDecoration(color: tone, shape: BoxShape.circle)),
          const SizedBox(width: 6),
          Text(label, style: tkText(size: 12, weight: 500, color: cssMix(tone, t.text, 0.82))),
        ],
      ),
    );
  }
}

/// The web's `button.small` in a list's row: a pill 36 px tall, 11.9 px words;
/// pine (lime on dark) when [primary], the danger colour's words when [danger].
class TkSmallButton extends StatelessWidget {
  const TkSmallButton({super.key, required this.label, this.onPressed, this.primary = false, this.danger = false, this.semantics});
  final String label;

  /// What a screen reader hears, when more than the words (the web's
  /// `sr-only` name after them).
  final String? semantics;
  final VoidCallback? onPressed;
  final bool primary;
  final bool danger;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return TapArea(child: Semantics(
      button: true,
      label: semantics,
      enabled: onPressed != null,
      onTap: onPressed,
      excludeSemantics: semantics != null,
      child: TkPress(
        onTap: onPressed ?? () {},
        child: Container(
          constraints: const BoxConstraints(minHeight: 36),
          padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 4),
          decoration: BoxDecoration(
            color: primary ? t.primary : (danger ? t.tkDangerBg : t.tkSoft),
            borderRadius: BorderRadius.circular(999),
          ),
          // As wide as its words, not the row it is in.
          child: Align(
            widthFactor: 1,
            heightFactor: 1,
            child: Text(
              label,
              style: tkText(size: 13.5, weight: 600, lineHeight: 1.2, color: primary ? t.primaryText : (danger ? t.tkDangerInk : t.text)),
            ),
          ),
        ),
      ),
    ));
  }
}
