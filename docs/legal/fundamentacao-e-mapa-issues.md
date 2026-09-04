# Fundamentação e mapa dos issues

Data da pesquisa: 4 de setembro de 2026. Este é um memorando de elaboração, não um parecer jurídico.

## Quadro considerado

- O [RGPD](https://eur-lex.europa.eu/eli/reg/2016/679/oj?locale=pt), em especial princípios e bases (artigos 5.º–7.º), crianças (8.º), saúde (9.º), transparência (12.º–14.º), direitos (15.º–22.º), destinatários (19.º), segurança/incidentes (32.º–34.º) e responsabilidade/contratos (24.º–30.º).
- A [Lei n.º 58/2019](https://diariodarepublica.pt/dr/detalhe/lei/58-2019-123815982), incluindo a idade de 13 anos para o consentimento de crianças em oferta direta de serviços da sociedade da informação. Esta regra é específica e não torna consentimento a base universal da atividade do clube.
- A informação da [CNPD sobre exercício dos direitos](https://www.cnpd.pt/cidadaos/direitos/) e [apagamento](https://www.cnpd.pt/cidadaos/direitos/direito-ao-apagamento-dos-dados/): resposta normal em um mês e exceções ao apagamento.
- Código Civil português, incluindo direitos de personalidade/direito à imagem e responsabilidades parentais. O texto de menor evita transformar representação em renúncia de responsabilidade.
- Diretiva 2002/58/CE/ePrivacy e legislação portuguesa aplicável ao armazenamento/acesso terminal: mecanismos estritamente necessários não justificam um banner sem escolha; mecanismos opcionais exigem avaliação e escolha prévia.

## Exemplos comparados

Foram consultadas políticas e modelos públicos de organizações desportivas portuguesas, incluindo [Sport Rugby Porto](https://www.sportrugby.pt/politica-privacidade.html), [FPDD — modelo para menores](https://fpdd.pt/novo/wp-content/uploads/2024/03/FPDD-Modelo-6-Declaracao-Consentimento-Dados-Pessoais-RGPD-Menores-de-idade.pdf), [Ginásio Clube Português](https://gcp.pt/wp-content/uploads/2020/02/GCP-Poli%CC%81tica-de-Privacidade.pdf) e a [política existente do CFC para cfcoimbra.com](https://cfcoimbra.com/politica-privacidade/).

Os exemplos ajudaram a identificar tópicos, mas não foram tratados como autoridade nem copiados. O pacote corrige padrões problemáticos comuns: «aceitar» uma política informativa, agrupar imagem com inscrição, cessão ilimitada, retenção vaga e autorização parental usada como exoneração geral.

## Correspondência com GitHub

| Issue | Entrega documental |
|---|---|
| [#218](https://github.com/ricardoespsanto/mycfc/issues/218) | Termos gerais, autorização de imagem e responsabilidade de menor, com rotas públicas propostas |
| [#109](https://github.com/ricardoespsanto/mycfc/issues/109) | Privacidade, cookies, direitos, versão para menores, inventário/matriz e registo de adoção |
| [#108](https://github.com/ricardoespsanto/mycfc/issues/108) | Modelo global de transparência/apagamento e decisões dependente/tutor, destinatários e backups |
| [#110](https://github.com/ricardoespsanto/mycfc/issues/110) | Procedimento de pedidos, identidade, revisão, prazos, decisões e acesso |
| [#111](https://github.com/ricardoespsanto/mycfc/issues/111) | Procedimento idempotente de apagamento, objetos, fornecedores, tombstones e restauro |
| [#150–#152](https://github.com/ricardoespsanto/mycfc/issues/150) | Procedimento de fotografias privadas e prazos propostos |

Os documentos não implementam rotas nem encerram issues. Depois da aprovação humana, #109/#218 exigem uma história de publicação, configuração, navegação, acessibilidade e testes.

## Limites e validações obrigatórias

Os textos não determinam sozinhos qual condição do artigo 9.º permite cada dado médico, qual prazo fiscal/federativo/segurador se aplica, nem validam contratos internacionais dos prestadores. Essas decisões dependem das operações reais, contratos e aconselhamento competente. Por isso aparecem como gates no registo de adoção e não como factos inventados.
