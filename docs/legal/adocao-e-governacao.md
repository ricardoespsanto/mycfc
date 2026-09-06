# Registo de adoção e governação dos documentos MyCFCoimbra

Estado: **por aprovar**

## Deliberação

Órgão competente: **[Direção/órgão a confirmar]**
Reunião e ata: **[preencher]**
Data de aprovação: **[preencher]**
Entrada em vigor: **[preencher]**
Responsável de privacidade: **[nome/função, não publicar nome se desnecessário]**
Canal de direitos aprovado: **[preencher]**
Revisão jurídica por: **[preencher ou indicar não realizada]**

## Registo de versões

| ID de configuração/documento | Ficheiro | Versão aprovada | URL canónico | SHA-256 | Substitui | Aprovação |
|---|---|---|---|---|---|---|
| `Termos_Gerais` | `termos-gerais.md` | [ ] | `/legal/termos-gerais` | [calcular sobre bytes publicados] | [ ] | [ ] |
| `Uso_Imagem` | `autorizacao-imagem.md` | [ ] | `/legal/uso-imagem` | [calcular] | [ ] | [ ] |
| `Responsabilidade_Menor` | `responsabilidade-menor.md` | [ ] | `/legal/responsabilidade-menor` | [calcular] | [ ] | [ ] |
| `Privacidade` | `politica-privacidade.md` | [ ] | `/legal/privacidade` | [calcular] | [ ] | [ ] |
| `Cookies` | `politica-cookies.md` | [ ] | `/legal/cookies` | [calcular] | [ ] | [ ] |
| `Direitos` | `exercicio-direitos.md` | [ ] | `/legal/direitos` | [calcular] | [ ] | [ ] |
| `Privacidade_Menores` | `privacidade-menores.md` | [ ] | `/legal/privacidade-menores` | [calcular] | [ ] | [ ] |

Conservar cada versão aprovada de forma imutável, exatamente nos bytes usados para o hash. Uma correção de conteúdo cria nova versão/hash; uma alteração apenas de apresentação que não muda os bytes canónicos deve ser documentada.

## Decisões obrigatórias antes de publicar

- [ ] Confirmar identidade, NIPC e morada do responsável.
- [ ] Aprovar canal próprio de direitos e responsável operacional.
- [ ] Decidir fundamento e salvaguardas de cada tratamento de saúde.
- [ ] Tornar autorização de imagem facultativa e granular, ou obter parecer jurídico escrito que justifique outra opção; o sistema atual exige-a no registo.
- [ ] Aprovar finalidades/canais da imagem e tratamento da maioridade/retirada.
- [ ] Confirmar legitimidade, conflitos e transição dos responsáveis por menores.
- [ ] Validar cookies/armazenamento real, Cloudflare e Turnstile.
- [ ] Aprovar todos os prazos e bases da matriz, incluindo backup diário 30 dias/mensal 365 dias.
- [ ] Corrigir expiração de versões não correntes dos backups/objetos e implementar repetição de tombstones no restauro antes de prometer apagamento completo.
- [ ] Aprovar lista/contratos/localizações de prestadores e transferências.
- [ ] Nomear autoridade de casos de privacidade distinta do simples acesso administrativo.
- [ ] Aprovar o momento de bloqueio de conta e prazo da prova mínima de pedidos.
- [ ] Harmonizar a política de `cfcoimbra.com` com o MyCFCoimbra.

## Revisão

Revisão ordinária anual e extraordinária antes de nova categoria de dados, fornecedor, país, finalidade, pagamento, transporte, álbum privado, wearable, análise, publicidade ou alteração legal. Mudança material exige avaliação da informação aos titulares; mudança de finalidade baseada em consentimento pode exigir novo consentimento.

## Referências de aprovação técnica

Após a deliberação, calcular hashes com a mesma representação canónica servida em produção, atualizar configuração e testar todas as rotas públicas. A publicação e qualquer alteração de issues/PR dependem de autorização humana própria; este documento não a concede.
