// The web's motion in the app (lib/kit/motion.dart): each moment takes the
// web's times and curves, and with motion turned down nothing moves.

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/chat/cards.dart';
import 'package:taskiem/chat/thread.dart';
import 'package:taskiem/kit/kit.dart';
import 'package:taskiem/kit/list.dart';
import 'package:taskiem/kit/modal.dart';
import 'package:taskiem/kit/motion.dart';
import 'package:taskiem/kit/sheet.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'support.dart';

Widget host(Widget child, {bool still = false}) => TaskiemApp(
  home: Builder(
    builder: (context) => MediaQuery(
      data: MediaQuery.of(context).copyWith(disableAnimations: still),
      child: Center(child: child),
    ),
  ),
);

/// The opacity and the matrix the nearest motion wrapper above [f] draws.
double opacityOf(WidgetTester t, Finder f) {
  final o = find.ancestor(of: f, matching: find.byType(Opacity));
  return o.evaluate().isEmpty ? 1 : t.widget<Opacity>(o.first).opacity;
}

Offset placeOf(WidgetTester t, Finder f) => t.getTopLeft(f);

void main() {
  setUpAll(loadFonts);

  testWidgets('a press gives as the web\'s: 0.97 over 90 ms', (t) async {
    await t.pumpWidget(host(TkPress(onTap: () {}, child: const SizedBox(width: 100, height: 40, child: Text('Go')))));
    final g = await t.startGesture(t.getCenter(find.text('Go')));
    await t.pump();
    await t.pump(Tk.tPress);
    expect(t.widget<AnimatedScale>(find.byType(AnimatedScale)).scale, Tk.pressScale);
    expect(t.widget<AnimatedScale>(find.byType(AnimatedScale)).duration, Tk.tPress);
    await g.up();
    await t.pumpAndSettle();
  });

  testWidgets('a list rises one after another (tk-stagger), and is simply there with motion turned down', (t) async {
    await t.pumpWidget(host(Column(children: [for (var i = 0; i < 3; i++) TkIn.stagger(index: i, child: Text('row $i'))])));
    expect(opacityOf(t, find.text('row 0')), 0);
    await t.pump(const Duration(milliseconds: 60));
    expect(opacityOf(t, find.text('row 0')), greaterThan(0));
    expect(opacityOf(t, find.text('row 2')), 0, reason: 'the third waits 80 ms');
    await t.pumpAndSettle();
    expect(opacityOf(t, find.text('row 2')), 1, reason: 'at rest');

    await t.pumpWidget(const SizedBox());
    await t.pumpWidget(host(Column(children: [for (var i = 0; i < 3; i++) TkIn.stagger(index: i, child: Text('row $i'))]), still: true));
    await t.pump();
    expect(opacityOf(t, find.text('row 2')), 1, reason: 'simply there');
  });

  testWidgets('a page\'s part is mostly seen within 100 ms (the fade eased as the web eases it, not linear)', (t) async {
    await t.pumpWidget(host(TkIn.rise(child: const Text('Money in today'))));
    await t.pump();
    await t.pump(const Duration(milliseconds: 100));
    expect(opacityOf(t, find.text('Money in today')), greaterThan(0.5));
    await t.pumpAndSettle();
  });

  testWidgets('a dialog springs up from the bottom over a fading veil, and drops away', (t) async {
    late BuildContext ctx;
    await t.pumpWidget(TaskiemApp(home: Builder(builder: (c) {
      ctx = c;
      return const SizedBox();
    })));
    final done = showTkModal<void>(ctx, title: 'Delete it?', builder: (_) => const Text('It cannot be undone.'));
    await t.pump();
    await t.pump(const Duration(milliseconds: 1));
    final screen = t.view.physicalSize.height / t.view.devicePixelRatio;
    expect(placeOf(t, find.text('Delete it?')).dy, greaterThan(screen - 40), reason: 'starts below the screen');
    await t.pump(const Duration(milliseconds: 180));
    final mid = placeOf(t, find.text('Delete it?')).dy;
    expect(mid, lessThan(screen));
    await t.pumpAndSettle();
    final rest = placeOf(t, find.text('Delete it?')).dy;
    expect(rest, lessThan(mid));
    Navigator.of(ctx).pop();
    await t.pump();
    await t.pump(const Duration(milliseconds: 100));
    expect(placeOf(t, find.text('Delete it?')).dy, greaterThan(rest), reason: 'leaving, it drops');
    await t.pumpAndSettle();
    expect(find.text('Delete it?'), findsNothing);
    await done;
  });

  testWidgets('a toast arrives with a little bounce (tk-arrive)', (t) async {
    late BuildContext ctx;
    await t.pumpWidget(TaskiemApp(home: Builder(builder: (c) {
      ctx = c;
      return const SizedBox();
    })));
    showTkToast(ctx, 'Saved');
    await t.pump();
    expect(opacityOf(t, find.text('Saved')), 0);
    await t.pump(const Duration(milliseconds: 200));
    expect(opacityOf(t, find.text('Saved')), greaterThan(0));
    await t.pump(Tk.tArrive);
    expect(find.text('Saved'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('the saved tick pops, then draws itself', (t) async {
    await t.pumpWidget(host(const SizedBox(width: 300, height: 300, child: Stack(children: [TkSavedTick()]))));
    CustomPaint paint() => t.widget<CustomPaint>(find.descendant(of: find.byType(TkSavedTick), matching: find.byType(CustomPaint)).first);
    dynamic p = paint().painter;
    expect(p.pop, 0);
    expect(p.draw, 0);
    await t.pump(const Duration(milliseconds: 150));
    p = paint().painter;
    expect(p.pop, greaterThan(0));
    expect(p.draw, 0, reason: 'the tick waits 200 ms');
    await t.pumpAndSettle();
    p = paint().painter;
    expect(p.draw, 1);
  });

  testWidgets('a fresh diary line lands lit in lime and settles to the panel; one already there is just shown', (t) async {
    Widget line(bool fresh) => host(TkFresh(fresh: fresh, child: const DiaryLine(words: 'Price of Rice: ₦76,000 → ₦78,000', when: 'You, 10:42')));
    await t.pumpWidget(line(true));
    BoxDecoration deco() => t.widget<DecoratedBox>(find.descendant(of: find.byType(TkLand), matching: find.byType(DecoratedBox)).first).decoration as BoxDecoration;
    await t.pump(const Duration(milliseconds: 400));
    expect(deco().color, const Color(0xFFC8F169), reason: 'lit in lime as it lands');
    await t.pumpAndSettle();
    final panel = TkTheme.tokens(t.element(find.byType(DiaryLine))).panel;
    expect(deco().color, panel);
    expect(find.byType(TkRoll), findsOneWidget, reason: 'its new value rolled into place');

    await t.pumpWidget(const SizedBox());
    await t.pumpWidget(line(false));
    await t.pump();
    expect(deco().color, panel);
    expect(find.byType(TkRoll), findsNothing);
    expect(find.textContaining('₦78,000'), findsOneWidget);
  });

  testWidgets('a message arriving slides in from its own side', (t) async {
    Future<double> startX({required bool mine}) async {
      await t.pumpWidget(const SizedBox());
      await t.pumpWidget(host(SizedBox(width: 300, child: MessageArrive(animate: true, mine: mine, child: const Text('Hello')))));
      await t.pump(const Duration(milliseconds: 16));
      final x = placeOf(t, find.text('Hello')).dx;
      await t.pumpAndSettle();
      return x - placeOf(t, find.text('Hello')).dx;
    }

    expect(await startX(mine: true), greaterThan(0), reason: 'one\'s own from the right');
    expect(await startX(mine: false), lessThan(0), reason: 'theirs from the left');

    await t.pumpWidget(const SizedBox());
    await t.pumpWidget(host(const MessageArrive(animate: false, mine: true, child: Text('Hello'))));
    expect(find.byType(TkIn), findsNothing, reason: 'there already: just shown');
  });

  testWidgets('a live dot pulses, and stops with motion turned down', (t) async {
    tkRepeatingMotion = true;
    addTearDown(() => tkRepeatingMotion = false);
    Widget dot({bool still = false}) => host(const TkPulse(color: Color(0xFFC8F169), period: Duration(seconds: 2), child: SizedBox(width: 10, height: 10)), still: still);
    await t.pumpWidget(dot());
    await t.pump();
    await t.pump(const Duration(milliseconds: 500));
    final ring = t.widget<DecoratedBox>(find.descendant(of: find.byType(TkPulse), matching: find.byType(DecoratedBox)).first);
    expect((ring.decoration as BoxDecoration).boxShadow, isNotEmpty);
    await t.pumpWidget(dot(still: true));
    await t.pump();
    final still = t.widget<DecoratedBox>(find.descendant(of: find.byType(TkPulse), matching: find.byType(DecoratedBox)).first);
    expect((still.decoration as BoxDecoration).boxShadow, isNull);
    await t.pumpWidget(const SizedBox());
  });

  testWidgets('a number rolls up when it changes', (t) async {
    Widget n(String v) => host(TkRoll(value: v, onShow: false, child: Text(v)));
    double shift() => t.widget<FractionalTranslation>(find.descendant(of: find.byType(TkRoll), matching: find.byType(FractionalTranslation))).translation.dy;
    await t.pumpWidget(n('3 waiting'));
    expect(shift(), 0, reason: 'not on first show');
    await t.pumpWidget(n('4 waiting'));
    await t.pump(const Duration(milliseconds: 50));
    expect(shift(), greaterThan(0));
    await t.pumpAndSettle();
    expect(shift(), 0);
  });

  testWidgets('what rises in keeps what it holds once at rest (a part is not made afresh)', (t) async {
    await t.pumpWidget(host(TkIn.stagger(child: const SizedBox(width: 200, child: EditableTextHost()))));
    await t.enterText(find.byType(EditableText), 'typed');
    await t.pumpAndSettle();
    expect(find.text('typed'), findsOneWidget);
  });

  testWidgets('an error rises in, and an empty list\'s icon pops', (t) async {
    await t.pumpWidget(host(const Column(mainAxisSize: MainAxisSize.min, children: [TkErrorBox('That didn’t go through.')])));
    expect(opacityOf(t, find.text('That didn’t go through.')), 0);
    await t.pumpAndSettle();
    expect(find.text('That didn’t go through.'), findsOneWidget);
    await t.pumpWidget(host(const TkEmpty(title: 'Nothing yet')));
    expect(find.byType(TkIn), findsOneWidget);
    await t.pumpAndSettle();
  });
}

/// A field whose words live in its own state.
class EditableTextHost extends StatefulWidget {
  const EditableTextHost({super.key});

  @override
  State<EditableTextHost> createState() => _EditableTextHostState();
}

class _EditableTextHostState extends State<EditableTextHost> {
  final _c = TextEditingController();
  final _f = FocusNode();

  @override
  void dispose() {
    _c.dispose();
    _f.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => EditableText(
    controller: _c,
    focusNode: _f,
    style: const TextStyle(fontSize: 14),
    cursorColor: const Color(0xFF000000),
    backgroundCursorColor: const Color(0xFF000000),
  );
}
