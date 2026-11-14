// The sheet a tool opens in (web/src/kit.tsx Sheet; kit.css .tk-sheet,
// .tk-veil; chat.css's motion), as a phone shows it: up from the bottom to
// 84 px under the top, over a dimmed and softly blurred veil; the grab bar,
// the head with its title and Close, what it holds, and its foot. It rises
// in 0.42 s on the spring and leaves down in --t-picker; with motion turned
// down it is there at once. A tap on the veil, Close or Back closes it.

import 'dart:ui' as ui;

import 'package:flutter/widgets.dart';

import 'motion.dart';
import '../theme/theme.dart';
import 'icon.dart';
import 'kit.dart';
import 'tap_area.dart';

/// Opens a sheet over the screen; the future ends when it closes, with
/// what it closed with.
Future<T?> showTkSheet<T>(BuildContext context, WidgetBuilder builder) {
  final still = MediaQuery.maybeDisableAnimationsOf(context) ?? false;
  return Navigator.of(context).push<T>(
    PageRouteBuilder<T>(
      opaque: false,
      barrierDismissible: false,
      // chat.css: `.tk-sheet { animation: tkd-sheet 0.42s cubic-bezier(0.2,
      // 0.95, 0.25, 1.04) }`, `.tk-veil { animation: tkd-dim 0.3s }`;
      // leaving, `tk-sheet-down var(--t-picker) var(--ease-out)`.
      transitionDuration: still ? Duration.zero : const Duration(milliseconds: 420),
      reverseTransitionDuration: still ? Duration.zero : Tk.tPicker,
      pageBuilder: (context, _, _) => builder(context),
      transitionsBuilder: (context, a, _, child) => _SheetMotion(animation: a, child: child),
    ),
  );
}

/// The sheet's motion: the veil fades, the sheet rises from below.
class _SheetMotion extends StatelessWidget {
  const _SheetMotion({required this.animation, required this.child});
  final Animation<double> animation;
  final Widget child;

  @override
  Widget build(BuildContext context) {
    return AnimatedBuilder(
      animation: animation,
      builder: (context, _) {
        final leaving = animation.status == AnimationStatus.reverse;
        final rise = leaving ? Tk.easeOut.transform(animation.value) : Tk.spring.transform(animation.value);
        final dim = leaving ? animation.value : (animation.value * 420 / 300).clamp(0.0, 1.0);
        return _SheetFrame(rise: rise, dim: dim, child: child);
      },
    );
  }
}

class _SheetFrame extends InheritedWidget {
  const _SheetFrame({required this.rise, required this.dim, required super.child});
  final double rise;
  final double dim;

  @override
  bool updateShouldNotify(_SheetFrame old) => old.rise != rise || old.dim != dim;
}

/// kit.tsx useSheetStep: a step open inside a sheet (a product's sizes over
/// its form, a bill's "paid outside" over the bill). While it is there the
/// sheet's Back, and the phone's own back, come back from it first, however
/// deep it is.
class TkSheetStep extends StatefulWidget {
  const TkSheetStep({super.key, this.open = true, required this.onBack, required this.child});

  /// Whether the step is open now (kept in place either way, so what is
  /// typed under it stays).
  final bool open;
  final VoidCallback onBack;
  final Widget child;

  @override
  State<TkSheetStep> createState() => _TkSheetStepState();
}

class _TkSheetStepState extends State<TkSheetStep> {
  _TkSheetState? _sheet;
  bool _in = false;

  void _back() => widget.onBack();

  /// Into the sheet's steps while open, out when not; after the frame (the
  /// sheet is being built when its steps are).
  void _sync() {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) return;
      final want = widget.open && _sheet != null;
      if (want == _in) return;
      _in = want;
      want ? _sheet!._add(_back) : _sheet?._remove(_back);
    });
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final sheet = context.dependOnInheritedWidgetOfExactType<_SheetSteps>()?.sheet;
    if (sheet == _sheet) return _sync();
    if (_in) _sheet?._remove(_back);
    _in = false;
    _sheet = sheet;
    _sync();
  }

  @override
  void didUpdateWidget(TkSheetStep old) {
    super.didUpdateWidget(old);
    if (old.open != widget.open) _sync();
  }

  @override
  void dispose() {
    final sheet = _sheet;
    if (_in) WidgetsBinding.instance.addPostFrameCallback((_) => sheet?._remove(_back));
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => widget.child;
}

class _SheetSteps extends InheritedWidget {
  const _SheetSteps({required this.sheet, required super.child});
  final _TkSheetState sheet;

  @override
  bool updateShouldNotify(_SheetSteps old) => old.sheet != sheet;
}

/// kit.tsx Sheet. Pulled up by its grab bar or head it takes the whole
/// screen; down again, its own height; thrown down, or pulled past a third
/// of its height, it closes; let go early, it springs back.
class TkSheet extends StatefulWidget {
  const TkSheet({super.key, required this.title, required this.body, this.footer, this.onBack, this.overlay, this.semanticsLabel, this.count, this.crumb, this.lead, this.sub, this.bareFooter = false});

  final String title;

  /// What it holds, scrolling under the head.
  final Widget body;

  /// `.tk-sheet-foot`: under a line, at the bottom.
  final Widget? footer;

  /// A step back inside the sheet (a form to its list): a round Back.
  final VoidCallback? onBack;

  /// Over everything in the sheet: the saved tick.
  final Widget? overlay;
  final String? semanticsLabel;

  /// `.tk-sheet-count`: how many the list holds, after the title.
  final int? count;

  /// What the title is in ("Products" over a product's name).
  final String? crumb;

  /// chat.css `.tk-sheet-lead`: a 48 px mark before the title (the title
  /// then 22 px), and `.tk-sheet-sub`, a line under it (the Buyer design).
  final Widget? lead;
  final String? sub;

  /// kit.css `.tk-sheet-foot:has(.tk-bag)`: no line above, no top padding.
  final bool bareFooter;

  @override
  State<TkSheet> createState() => _TkSheetState();
}

class _TkSheetState extends State<TkSheet> with SingleTickerProviderStateMixin {
  final _steps = <VoidCallback>[];
  bool _full = false;

  /// How far the finger has it moved (down is positive).
  double _drag = 0;
  bool _dragging = false;

  /// The way back from where it was let go.
  late final AnimationController _settle = AnimationController(vsync: this, duration: Tk.tPicker)..addListener(() => setState(() => _drag = _from * (1 - Tk.springSettle.transform(_settle.value))));
  double _from = 0;

  void _add(VoidCallback back) => setState(() => _steps.add(back));
  void _remove(VoidCallback back) {
    if (!mounted) return;
    setState(() => _steps.remove(back));
  }

  @override
  void dispose() {
    _settle.dispose();
    super.dispose();
  }

  void _pull(DragUpdateDetails d) {
    _settle.stop();
    setState(() {
      _dragging = true;
      _drag += d.delta.dy;
    });
  }

  void _letGo(DragEndDetails d, double height, VoidCallback close) {
    final v = d.primaryVelocity ?? 0;
    final dy = _drag;
    setState(() => _dragging = false);
    if (!_full && dy < -48) {
      _full = true;
    } else if (_full && dy > 48 && v < 700 && dy < height / 2) {
      _full = false;
    } else if (v > 700 || dy > height / 3) {
      close();
      return;
    }
    _from = dy;
    if (MediaQuery.maybeDisableAnimationsOf(context) ?? false) {
      setState(() => _drag = 0);
    } else {
      _settle.forward(from: 0);
    }
  }

  @override
  Widget build(BuildContext context) {
    final title = widget.title;
    final body = widget.body;
    final footer = widget.footer;
    final overlay = widget.overlay;
    final semanticsLabel = widget.semanticsLabel;
    final count = widget.count;
    final lead = widget.lead;
    final sub = widget.sub;
    final bareFooter = widget.bareFooter;
    // Back: out of the deepest step open inside, else the sheet's own.
    final onBack = _steps.isNotEmpty ? _steps.last : widget.onBack;
    // "Products / Jollof rice" names the list's row, not a step inside it.
    final crumb = _steps.isNotEmpty && widget.onBack != null ? null : widget.crumb;
    final t = TkTheme.tokens(context);
    final frame = context.dependOnInheritedWidgetOfExactType<_SheetFrame>();
    final rise = frame?.rise ?? 1;
    final dim = frame?.dim ?? 1;
    final pad = MediaQuery.paddingOf(context);
    void close() => Navigator.of(context).pop();
    final big = MediaQuery.textScalerOf(context).scale(10) > 15;
    final Widget headRow = Padding(
                            padding: const EdgeInsets.fromLTRB(20, 6, 20, 12),
                            child: Row(
                              children: [
                                if (onBack != null && crumb == null) ...[
                                  _Round(icon: 'back', label: 'Back', onTap: onBack),
                                  // `.tk-back { margin-right: 4px }` with the head's 12 px gap.
                                  const SizedBox(width: 16),
                                ],
                                // `.tk-sheet-head.crumbed`: "Products / Jollof rice", the
                                // first word the way back.
                                if (onBack != null && crumb != null) ...[
                                  TapArea(
                                    child: Semantics(
                                      button: true,
                                      label: '$crumb: back to the list',
                                      onTap: onBack,
                                      excludeSemantics: true,
                                      child: GestureDetector(
                                        behavior: HitTestBehavior.opaque,
                                        onTap: onBack,
                                        child: ConstrainedBox(
                                          constraints: const BoxConstraints(minHeight: 32),
                                          child: Row(
                                            mainAxisSize: MainAxisSize.min,
                                            children: [
                                              Text(crumb, style: tkText(size: 14, weight: 500, color: t.muted)),
                                              const SizedBox(width: 8),
                                              Text('/', style: tkText(size: 14, weight: 500, color: t.muted)),
                                              const SizedBox(width: 2),
                                            ],
                                          ),
                                        ),
                                      ),
                                    ),
                                  ),
                                  const SizedBox(width: 6),
                                ],
                                if (lead != null) ...[
                                  SizedBox(width: 48, height: 48, child: ExcludeSemantics(child: lead)),
                                  const SizedBox(width: 12),
                                ],
                                Expanded(
                                  child: Semantics(
                                    header: true,
                                    child: Column(
                                      crossAxisAlignment: CrossAxisAlignment.start,
                                      children: [
                                        if (crumb != null && onBack == null) Text(crumb, style: tkText(size: 13, weight: 600, color: t.muted)),
                                        Text.rich(
                                          TextSpan(
                                            text: title,
                                            children: [
                                              // `.tk-sheet-count { font-size: 14px; font-weight: 400; color: var(--muted) }`, 8 px after.
                                              if (count != null) ...[
                                                const WidgetSpan(child: SizedBox(width: 8)),
                                                TextSpan(text: '$count', style: tkText(size: 14, weight: 400, letterSpacing: 0, color: t.muted)),
                                              ],
                                            ],
                                          ),
                                          style: onBack != null && crumb != null
                                              // `.tk-sheet-head.crumbed h2 { font-size: 14px; font-weight: 700; letter-spacing: 0 }`
                                              ? tkText(size: 14, weight: 700, lineHeight: 1.35, color: t.text)
                                              : lead != null
                                              ? tkText(size: 22, weight: 800, lineHeight: 1.2, letterSpacing: -0.44, color: t.text)
                                              : tkText(size: 26, weight: 800, lineHeight: 1.35, letterSpacing: -0.52, color: t.text),
                                        ),
                                        if (sub != null) ...[
                                          const SizedBox(height: 2),
                                          Text(sub, style: tkText(size: 13, color: t.muted)),
                                        ],
                                      ],
                                    ),
                                  ),
                                ),
                                const SizedBox(width: 12),
                                _Round(icon: 'close', label: 'Close', onTap: close),
                              ],
                            ),
                          );
    return PopScope(
      // The phone's back steps back inside the sheet first.
      canPop: onBack == null,
      onPopInvokedWithResult: (didPop, _) {
        if (!didPop) onBack?.call();
      },
      child: _SheetSteps(
        sheet: this,
        child: LayoutBuilder(
          builder: (context, screen) {
            final top = _full ? pad.top : 84 + pad.top;
            final height = screen.maxHeight - top;
            // Up only a little past where it can go (it gives, then stops).
            final room = _full ? 0.0 : 84.0;
            final lift = _drag >= 0 ? _drag : -((-_drag).clamp(0.0, room) + ((-_drag - room).clamp(0.0, 120.0) / 6));
            final pulled = GestureDetector(
              behavior: HitTestBehavior.opaque,
              onVerticalDragUpdate: _pull,
              onVerticalDragEnd: (d) => _letGo(d, height, close),
              onVerticalDragCancel: () => _letGo(DragEndDetails(primaryVelocity: 0), height, close),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                mainAxisSize: MainAxisSize.min,
                children: [
                  // `.tk-sheet-grab`.
                  Padding(
                    padding: const EdgeInsets.only(top: 10, bottom: 4),
                    child: Center(
                      child: Container(
                        width: 40,
                        height: 5,
                        decoration: BoxDecoration(color: t.borderStrong, borderRadius: BorderRadius.circular(3)),
                      ),
                    ),
                  ),
                  if (!big) headRow,
                ],
              ),
            );
            return _sheet(context, t, top: top, lift: lift, pulled: pulled, headRow: headRow, big: big, close: close, rise: rise, dim: dim,
                title: title, body: body, footer: footer, overlay: overlay, semanticsLabel: semanticsLabel, bareFooter: bareFooter);
          },
        ),
      ),
    );
  }

  Widget _sheet(
    BuildContext context,
    TkTokens t, {
    required double top,
    required double lift,
    required Widget pulled,
    required Widget headRow,
    required bool big,
    required VoidCallback close,
    required double rise,
    required double dim,
    required String title,
    required Widget body,
    required Widget? footer,
    required Widget? overlay,
    required String? semanticsLabel,
    required bool bareFooter,
  }) {
    final head = headRow;
    return Stack(
      children: [
        // `.tk-veil { background: var(--tk-veil); backdrop-filter: blur(2px) }`
        Positioned.fill(
          child: Backdrop(
            behavior: HitTestBehavior.deferToChild,
            onClose: close,
            child: Opacity(
              opacity: dim,
              child: BackdropFilter(
                filter: ui.ImageFilter.blur(sigmaX: 2, sigmaY: 2),
                child: ColoredBox(color: t.tkVeil),
              ),
            ),
          ),
        ),
        AnimatedPositioned(
          duration: _dragging || (MediaQuery.maybeDisableAnimationsOf(context) ?? false) ? Duration.zero : Tk.tPicker,
          curve: Tk.springSettle,
          left: 0,
          right: 0,
          bottom: 0,
          // `top: 84px`; pulled up, the whole screen.
          top: top,
          child: LayoutBuilder(
            builder: (context, box) => Transform.translate(
              offset: Offset(0, (1 - rise) * box.maxHeight + lift),
              child: Semantics(
                scopesRoute: true,
                namesRoute: true,
                explicitChildNodes: true,
                label: semanticsLabel ?? title,
                child: Container(
                  clipBehavior: Clip.antiAlias,
                  decoration: BoxDecoration(
                    color: t.panel,
                    borderRadius: BorderRadius.vertical(top: Radius.circular(_full ? 0 : 28)),
                    boxShadow: const [BoxShadow(color: Color(0x660E0F0C), offset: Offset(0, -20), blurRadius: 50, spreadRadius: -20)],
                  ),
                  child: Stack(
                    children: [
                      Column(
                        crossAxisAlignment: CrossAxisAlignment.stretch,
                        children: [
                          // `.tk-sheet-grab` and the head: pulled, it grows, shrinks or closes.
                          pulled,
                          // With large text the head scrolls away with the body, which
                          // keeps room for itself (its own list scrolls in it).
                          Expanded(
                            child: big
                                ? SingleChildScrollView(
                                    child: Column(
                                      crossAxisAlignment: CrossAxisAlignment.stretch,
                                      children: [head, SizedBox(height: box.maxHeight * 0.62, child: body)],
                                    ),
                                  )
                                : body,
                          ),
                          if (footer != null) TkSheetFoot(bare: bareFooter, child: footer),
                        ],
                      ),
                      ?overlay,
                    ],
                  ),
                ),
              ),
            ),
          ),
        ),
      ],
    );
  }
}

/// `.tk-round`: the head's 40 px round button on the soft ground.
class _Round extends StatelessWidget {
  const _Round({required this.icon, required this.label, required this.onTap});
  final String icon;
  final String label;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return TapArea(child: Semantics(
      button: true,
      label: label,
      onTap: onTap,
      excludeSemantics: true,
      child: TkPress(
        onTap: onTap,
        child: Container(
          width: 40,
          height: 40,
          decoration: BoxDecoration(color: t.tkSoft, shape: BoxShape.circle),
          alignment: Alignment.center,
          child: TkIcon(icon, size: 18, color: t.text),
        ),
      ),
    ));
  }
}

/// `.tk-form-body`: a form in a sheet, scrolling: 18 px between its parts.
class TkFormBody extends StatelessWidget {
  const TkFormBody({super.key, required this.children});
  final List<Widget> children;

  @override
  Widget build(BuildContext context) {
    return SingleChildScrollView(
      padding: const EdgeInsets.fromLTRB(20, 4, 20, 16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          for (var i = 0; i < children.length; i++) ...[if (i > 0) const SizedBox(height: 18), children[i]],
        ],
      ),
    );
  }
}

/// `.tk-form-now`: a line of what is so, muted.
TextStyle tkFormNow(TkTokens t) => tkText(size: 14, color: t.muted);

/// `.tk-lines`: label and value rows, 10 px apart.
class TkLines extends StatelessWidget {
  const TkLines(this.rows, {super.key});
  final List<(String, String)> rows;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        for (var i = 0; i < rows.length; i++) ...[
          if (i > 0) const SizedBox(height: 10),
          Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(rows[i].$1, style: tkText(size: 15, color: t.text)),
              const SizedBox(width: 12),
              Expanded(child: Text(rows[i].$2, textAlign: TextAlign.right, style: tkText(size: 15, weight: 700, color: t.text))),
            ],
          ),
        ],
      ],
    );
  }
}

/// kit.tsx KitField (`.tk-field`): the label, the box, an error, a hint.
class TkKitField extends StatelessWidget {
  const TkKitField({super.key, required this.label, required this.child, this.hint, this.error, this.field = true});
  final String label;
  final Widget child;

  /// [child] is a text box, named by [label] (not a row of buttons).
  final bool field;
  final String? hint;
  final String? error;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        // A field's words and its box are one to a screen reader; a row of
        // buttons keeps its own.
        _MergeIf(
          field,
          Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [Text(label, style: tkText(size: 13, weight: 600, color: t.muted)), const SizedBox(height: 6), child],
          ),
        ),
        if (error != null) ...[const SizedBox(height: 6), Text(error!, style: tkText(size: 13, weight: 600, color: t.danger))],
        if (hint != null) ...[const SizedBox(height: 6), Text(hint!, style: tkText(size: 13, color: t.muted))],
      ],
    );
  }
}

class _MergeIf extends StatelessWidget {
  const _MergeIf(this.merge, this.child);
  final bool merge;
  final Widget child;

  @override
  Widget build(BuildContext context) => merge ? MergeSemantics(child: child) : child;
}

/// kit.tsx SavedTick (`.tk-saved`): over the sheet, the lime tick and a word.
class TkSavedTick extends StatelessWidget {
  const TkSavedTick({super.key, this.words = 'Saved'});
  final String words;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Positioned.fill(
      child: Semantics(
        liveRegion: true,
        label: words,
        child: ColoredBox(
          color: cssMix(t.panel, const Color(0x00000000), 0.92),
          child: Column(
            mainAxisAlignment: MainAxisAlignment.center,
            children: [
              _TickIn(ground: t.brandDark, ink: t.brand),
              const SizedBox(height: 12),
              Text(words, style: tkText(size: 18, weight: 700, color: t.text)),
            ],
          ),
        ),
      ),
    );
  }
}

/// chat.css `.tk-tick`: the circle pops (tkd-pop, 0.45 s) and the tick
/// draws itself after 0.2 s (tkd-draw, 0.35 s).
class _TickIn extends StatefulWidget {
  const _TickIn({required this.ground, required this.ink});
  final Color ground;
  final Color ink;

  @override
  State<_TickIn> createState() => _TickInState();
}

class _TickInState extends State<_TickIn> with SingleTickerProviderStateMixin {
  late final _a = AnimationController(vsync: this, duration: const Duration(milliseconds: 550));
  late final _pop = CurvedAnimation(parent: _a, curve: const Interval(0, 450 / 550, curve: Cubic(0.2, 0.9, 0.25, 1.2)));
  late final _draw = CurvedAnimation(parent: _a, curve: const Interval(200 / 550, 1, curve: Curves.easeOut));

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (tkStill(context)) {
      _a.value = 1;
    } else if (_a.value == 0 && !_a.isAnimating) {
      _a.forward();
    }
  }

  @override
  void dispose() {
    _pop.dispose();
    _draw.dispose();
    _a.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => AnimatedBuilder(
    animation: _a,
    builder: (context, _) => CustomPaint(
      size: const Size(76, 76),
      painter: _Tick(widget.ground, widget.ink, pop: _pop.value, draw: _draw.value),
    ),
  );
}

class _Tick extends CustomPainter {
  _Tick(this.ground, this.ink, {this.pop = 1, this.draw = 1});
  final Color ground;
  final Color ink;

  /// How far the circle has popped (0 to 1, past 1 on the overshoot).
  final double pop;

  /// How much of the tick is drawn.
  final double draw;

  @override
  void paint(Canvas canvas, Size size) {
    final s = size.width / 52;
    canvas.scale(s);
    // tkd-pop: from 0.6 and faded, full by 60%.
    final scale = 0.6 + 0.4 * pop;
    canvas.save();
    canvas.translate(26, 26);
    canvas.scale(scale);
    canvas.translate(-26, -26);
    canvas.drawCircle(const Offset(26, 26), 24, Paint()..color = ground.withValues(alpha: ground.a * (pop / 0.6).clamp(0.0, 1.0)));
    canvas.restore();
    if (draw <= 0) return;
    final tick = Path()
      ..moveTo(15, 27)
      ..lineTo(22, 34)
      ..lineTo(37, 18);
    final drawn = Path();
    for (final m in tick.computeMetrics()) {
      drawn.addPath(m.extractPath(0, m.length * draw.clamp(0.0, 1.0)), Offset.zero);
    }
    canvas.drawPath(
      drawn,
      Paint()
        ..color = ink
        ..style = PaintingStyle.stroke
        ..strokeWidth = 4
        ..strokeCap = StrokeCap.round
        ..strokeJoin = StrokeJoin.round,
    );
  }

  @override
  bool shouldRepaint(_Tick old) => old.ground != ground || old.ink != ink || old.pop != pop || old.draw != draw;
}

/// `.tk-foot-two`: two buttons sharing the foot as CSS flex shares it
/// (`flex: 1`, a zero basis: equal shares beyond each one's own padding
/// and border).
class TkFootTwo extends StatelessWidget {
  const TkFootTwo({super.key, required this.first, required this.firstOwn, required this.second, required this.secondOwn});
  final Widget first;
  final Widget second;

  /// Each one's own width: its padding and border.
  final double firstOwn;
  final double secondOwn;

  @override
  Widget build(BuildContext context) {
    return LayoutBuilder(
      builder: (context, box) {
        final share = ((box.maxWidth - 10 - firstOwn - secondOwn) / 2).clamp(0.0, double.infinity);
        return Row(
          children: [
            SizedBox(width: firstOwn + share, child: first),
            const SizedBox(width: 10),
            SizedBox(width: secondOwn + share, child: second),
          ],
        );
      },
    );
  }
}

/// `.tk-btn.ghost`: on the panel, a line round it (padding 0 18 px).
class TkGhostButton extends StatelessWidget {
  const TkGhostButton({super.key, required this.label, this.onTap, this.danger = false});
  final String label;
  final VoidCallback? onTap;
  final bool danger;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    return Semantics(
      button: true,
      enabled: onTap != null,
      label: label,
      onTap: onTap,
      excludeSemantics: true,
      child: TkPress(
        onTap: onTap,
        child: Container(
          constraints: const BoxConstraints(minHeight: 56),
          padding: const EdgeInsets.symmetric(horizontal: 18),
          alignment: Alignment.center,
          decoration: BoxDecoration(color: t.panel, border: Border.all(color: t.borderStrong), borderRadius: BorderRadius.circular(999)),
          child: Text(label, style: tkText(size: 15, weight: 700, lineHeight: 1.2, color: danger ? t.danger : (dark ? t.text : t.brand))),
        ),
      ),
    );
  }
}

/// `.tk-cta`: the sheet's main button (`.tk-cta-danger` red), with an icon.
class TkCta extends StatelessWidget {
  const TkCta({super.key, required this.label, this.onTap, this.icon, this.danger = false, this.iconAfter = false});
  final String label;
  final VoidCallback? onTap;
  final String? icon;
  final bool danger;

  /// The icon after the words (an arrow), not before.
  final bool iconAfter;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    final (bg, ink) = danger ? (t.danger, t.tkOnPine) : (dark ? (t.brandDark, t.brandDarkText) : (t.brand, const Color(0xFFFFFFFF)));
    // `.tk-cta svg { color: var(--brand-dark) }`: lime on pine (pine on lime
    // in the dark theme).
    final mark = danger ? ink : (dark ? t.brandDarkText : t.brandDark);
    return Semantics(
      button: true,
      enabled: onTap != null,
      label: label,
      onTap: onTap,
      excludeSemantics: true,
      child: TkPress(
        onTap: onTap,
        child: Opacity(
          // `.tk-cta:disabled { opacity: 0.5 }`
          opacity: onTap == null ? 0.5 : 1,
          child: Container(
            constraints: const BoxConstraints(minHeight: 56),
            padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 6),
            alignment: Alignment.center,
            decoration: BoxDecoration(color: bg, borderRadius: BorderRadius.circular(999)),
            child: Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                if (icon != null && !iconAfter) ...[TkIcon(icon!, size: 20, color: mark), const SizedBox(width: 8)],
                Flexible(child: Text(label, textAlign: TextAlign.center, style: tkText(size: 16, weight: 700, lineHeight: 1.2, color: ink))),
                if (icon != null && iconAfter) ...[const SizedBox(width: 8), TkIcon(icon!, size: 20, color: mark)],
              ],
            ),
          ),
        ),
      ),
    );
  }
}

/// kit.css `.tk-sheet-foot`: the sheet's buttons, over a line, clear of
/// the phone's bottom edge; for a body that draws its own foot (a row's
/// detail whose buttons follow its state).
class TkSheetFoot extends StatelessWidget {
  const TkSheetFoot({super.key, required this.child, this.bare = false});
  final Widget child;
  final bool bare;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final pad = MediaQuery.paddingOf(context);
    return Container(
      decoration: bare ? null : BoxDecoration(border: Border(top: BorderSide(color: t.border))),
      padding: EdgeInsets.fromLTRB(20, bare ? 0 : 12, 20, 18 + pad.bottom),
      child: child,
    );
  }
}
